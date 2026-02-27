package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"kula-szpiegula/internal/collector"
	"kula-szpiegula/internal/config"
	"kula-szpiegula/internal/storage"
)

//go:embed static
var staticFS embed.FS

// Server is the HTTP/WebSocket server for the web UI.
type Server struct {
	cfg       config.WebConfig
	collector *collector.Collector
	store     *storage.Store
	auth      *AuthManager
	hub       *wsHub
}

func NewServer(cfg config.WebConfig, c *collector.Collector, s *storage.Store) *Server {
	srv := &Server{
		cfg:       cfg,
		collector: c,
		store:     s,
		auth:      NewAuthManager(cfg.Auth),
		hub:       newWSHub(),
	}
	return srv
}

// BroadcastSample sends a new sample to all WebSocket clients.
func (s *Server) BroadcastSample(sample *collector.Sample) {
	data, err := json.Marshal(sample)
	if err != nil {
		return
	}
	s.hub.broadcast(data)
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API routes
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/current", s.handleCurrent)
	apiMux.HandleFunc("/api/history", s.handleHistory)
	apiMux.HandleFunc("/api/config", s.handleConfig)
	apiMux.HandleFunc("/api/login", s.handleLogin)
	apiMux.HandleFunc("/api/auth/status", s.handleAuthStatus)

	// WebSocket
	apiMux.HandleFunc("/ws", s.handleWebSocket)

	// Apply auth to API routes (except login and auth status)
	mux.Handle("/api/login", apiMux)
	mux.Handle("/api/auth/status", apiMux)
	mux.Handle("/api/", s.auth.AuthMiddleware(apiMux))
	mux.Handle("/ws", s.auth.AuthMiddleware(apiMux))

	// Static files
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticContent)))

	// Start WebSocket hub
	go s.hub.run()

	// Session cleanup goroutine
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.auth.CleanupSessions()
		}
	}()

	addr := fmt.Sprintf("%s:%d", s.cfg.Listen, s.cfg.Port)
	log.Printf("Web UI starting on http://%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleCurrent(w http.ResponseWriter, r *http.Request) {
	sample := s.collector.Latest()
	if sample == nil {
		http.Error(w, `{"error":"no data yet"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sample)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	var from, to time.Time
	var err error

	if toStr == "" {
		to = time.Now()
	} else {
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			http.Error(w, `{"error":"invalid 'to' time"}`, http.StatusBadRequest)
			return
		}
	}

	if fromStr == "" {
		from = to.Add(-5 * time.Minute)
	} else {
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			http.Error(w, `{"error":"invalid 'from' time"}`, http.StatusBadRequest)
			return
		}
	}

	samples, err := s.store.QueryRange(from, to)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(samples)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"auth_enabled": s.cfg.Auth.Enabled,
		"version":      "0.1.0",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if !s.auth.ValidateCredentials(creds.Username, creds.Password) {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token := s.auth.CreateSession(creds.Username)

	http.SetCookie(w, &http.Cookie{
		Name:     "kula_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(s.cfg.Auth.SessionTimeout.Seconds()),
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"auth_required": s.cfg.Auth.Enabled,
		"authenticated": false,
	}

	if !s.cfg.Auth.Enabled {
		status["authenticated"] = true
	} else {
		cookie, err := r.Cookie("kula_session")
		if err == nil && s.auth.ValidateSession(cookie.Value) {
			status["authenticated"] = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// wsHub manages WebSocket connections
type wsHub struct {
	mu      sync.RWMutex
	clients map[*wsClient]bool
	regCh   chan *wsClient
	unregCh chan *wsClient
}

func newWSHub() *wsHub {
	return &wsHub{
		clients: make(map[*wsClient]bool),
		regCh:   make(chan *wsClient, 16),
		unregCh: make(chan *wsClient, 16),
	}
}

func (h *wsHub) run() {
	for {
		select {
		case client := <-h.regCh:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregCh:
			h.mu.Lock()
			delete(h.clients, client)
			h.mu.Unlock()
		}
	}
}

func (h *wsHub) broadcast(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if !client.paused {
			select {
			case client.sendCh <- data:
			default:
				// Client too slow, skip
			}
		}
	}
}
