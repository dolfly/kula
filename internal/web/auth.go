package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kula/internal/config"

	"golang.org/x/crypto/argon2"
)

// AuthManager handles authentication validation and sessions.

type AuthManager struct {
	mu          sync.RWMutex
	cfg         config.AuthConfig
	storageDir  string
	sessions    map[string]*session
	Limiter     *RateLimiter
	UserLimiter *RateLimiter
	trustProxy  bool
	security    config.SecurityConfig
}

// RateLimiter tracks recent rapid login attempts by IP.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

// maxRateLimiterKeys bounds how many distinct keys (client IPs or usernames) a
// rate limiter tracks at once. It sits far above any legitimate concurrent client
// count for a self-hosted monitor but caps memory if an attacker sprays requests
// from many source addresses. On reaching the cap the limiter purges stale entries
// and, if still saturated with fresh ones, refuses new keys (fail-closed).
const maxRateLimiterKeys = 16384

// reserveRateLimiterKey reports whether key may be tracked in m without growing it
// past maxRateLimiterKeys. Already-tracked keys are always admitted. A new key is
// admitted while there is headroom; once the map is full, purge is run to reclaim
// stale entries and the key is admitted only if that frees space. The caller must
// hold the limiter's lock, and purge must operate under that same held lock.
func reserveRateLimiterKey(m map[string][]time.Time, key string, purge func()) bool {
	if _, tracked := m[key]; tracked {
		return true
	}
	if len(m) >= maxRateLimiterKeys {
		purge()
		if len(m) >= maxRateLimiterKeys {
			return false
		}
	}
	return true
}

type session struct {
	username  string
	csrfToken string
	createdAt time.Time
	expiresAt time.Time
}

// sessionData is used for JSON serialization of sessions.
type sessionData struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	CSRFToken string    `json:"csrf_token,omitempty"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewAuthManager(cfg config.AuthConfig, storageDir string, trustProxy bool, security config.SecurityConfig) *AuthManager {
	return &AuthManager{
		cfg:        cfg,
		storageDir: storageDir,
		sessions:   make(map[string]*session),
		Limiter: &RateLimiter{
			attempts: make(map[string][]time.Time),
		},
		UserLimiter: &RateLimiter{
			attempts: make(map[string][]time.Time),
		},
		trustProxy: trustProxy,
		security:   security,
	}
}

// purge removes entries older than cutoff. Must be called with rl.mu held.
func (rl *RateLimiter) purge(cutoff time.Time) {
	for key, attempts := range rl.attempts {
		var recent []time.Time
		for _, t := range attempts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(rl.attempts, key)
		} else {
			rl.attempts[key] = recent
		}
	}
}

// Allow checks if the given key has exceeded 5 login attempts in the last 5 minutes.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)

	if !reserveRateLimiterKey(rl.attempts, ip, func() { rl.purge(cutoff) }) {
		return false
	}

	var recent []time.Time
	for _, t := range rl.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= 5 {
		return false
	}

	rl.attempts[ip] = append(recent, now)
	return true
}

// HashPassword creates an Argon2id hash with the given salt and parameters.
func HashPassword(password, salt string, params config.Argon2Config) string {
	keyLen := uint32(32)

	hash := argon2.IDKey([]byte(password), []byte(salt), params.Time, params.Memory, params.Threads, keyLen)
	return hex.EncodeToString(hash)
}

// hashToken returns a SHA-256 hash of the session token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// GenerateSalt creates a random 32-byte hex salt.
func GenerateSalt() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// dummySalt and dummyHash back the constant-time fallback in
// ValidateCredentials. When the supplied username matches no configured user,
// a throwaway Argon2id hash is still computed and compared against these fixed
// values so that an unknown username costs the same wall-clock time as a known
// one. This closes a user-enumeration timing oracle. The values are arbitrary
// constants; the comparison they feed is always expected to fail. dummyHash is
// 64 hex chars to match the length of a real HashPassword result so the
// constant-time compare runs over equal-length inputs.
const (
	dummySalt = "0000000000000000000000000000000000000000000000000000000000000000"
	dummyHash = "0000000000000000000000000000000000000000000000000000000000000000"
)

// ValidateCredentials checks username and password against config.
//
// Exactly one Argon2id computation is performed on every call regardless of
// whether the username exists: a non-matching username falls through to a
// throwaway hash over dummySalt so the response time does not reveal which
// usernames are valid (enumeration timing side channel).
func (a *AuthManager) ValidateCredentials(username, password string) bool {
	if !a.cfg.Enabled {
		return true
	}

	if subtle.ConstantTimeCompare([]byte(username), []byte(a.cfg.Username)) == 1 {
		hash := HashPassword(password, a.cfg.PasswordSalt, a.cfg.Argon2)
		return subtle.ConstantTimeCompare([]byte(hash), []byte(a.cfg.PasswordHash)) == 1
	}

	for _, u := range a.cfg.Users {
		if subtle.ConstantTimeCompare([]byte(username), []byte(u.Username)) == 1 {
			hash := HashPassword(password, u.PasswordSalt, a.cfg.Argon2)
			return subtle.ConstantTimeCompare([]byte(hash), []byte(u.PasswordHash)) == 1
		}
	}

	// No username matched. Perform a throwaway Argon2id computation with the
	// same parameters (and a constant-time compare) so an unknown username
	// costs the same time as a known one. Without this, the early return here
	// would make unknown usernames resolve ~1000x faster than known ones,
	// leaking which usernames exist.
	dummy := HashPassword(password, dummySalt, a.cfg.Argon2)
	_ = subtle.ConstantTimeCompare([]byte(dummy), []byte(dummyHash))
	return false
}

// CreateSession creates a new authenticated session.
func (a *AuthManager) CreateSession(username string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	token, err := generateToken()
	if err != nil {
		return "", err
	}
	csrfToken, err := generateToken()
	if err != nil {
		return "", err
	}

	hashedToken := hashToken(token)
	a.sessions[hashedToken] = &session{
		username:  username,
		csrfToken: csrfToken,
		createdAt: time.Now(),
		expiresAt: time.Now().Add(a.cfg.SessionTimeout),
	}

	return token, nil
}

// ValidateSession checks if a session token is valid and unexpired.
func (a *AuthManager) ValidateSession(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	hashedToken := hashToken(token)
	sess, ok := a.sessions[hashedToken]
	if !ok {
		return false
	}

	if time.Now().After(sess.expiresAt) {
		delete(a.sessions, hashedToken)
		return false
	}

	// Sliding expiration
	sess.expiresAt = time.Now().Add(a.cfg.SessionTimeout)

	return true
}

// GetCSRFToken retrieves the CSRF token associated with a session token.
func (a *AuthManager) GetCSRFToken(token string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	hashedToken := hashToken(token)
	if sess, ok := a.sessions[hashedToken]; ok {
		return sess.csrfToken
	}
	return ""
}

// RevokeSession manually destroys a session by its token.
func (a *AuthManager) RevokeSession(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hashedToken := hashToken(token)
	delete(a.sessions, hashedToken)
}

// AuthMiddleware protects routes when auth is enabled.
func (a *AuthManager) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.cfg.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		// Check cookie
		cookie, err := r.Cookie("kula_session")
		if err == nil && a.ValidateSession(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}

		// Check Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" && len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token := authHeader[7:]
			if a.ValidateSession(token) {
				next.ServeHTTP(w, r)
				return
			}
		}

		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	})
}

// CleanupSessions removes expired sessions and stale rate limiter entries periodically.
func (a *AuthManager) CleanupSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for token, sess := range a.sessions {
		if now.After(sess.expiresAt) {
			delete(a.sessions, token)
		}
	}

	// Purge stale rate limiter entries
	cutoff := now.Add(-5 * time.Minute)
	a.Limiter.mu.Lock()
	a.Limiter.purge(cutoff)
	a.Limiter.mu.Unlock()
	a.UserLimiter.mu.Lock()
	a.UserLimiter.purge(cutoff)
	a.UserLimiter.mu.Unlock()
}

// LoadSessions loads sessions from disk.
func (a *AuthManager) LoadSessions() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	path := filepath.Join(a.storageDir, "sessions.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No sessions to load
		}
		return err
	}

	var saved []sessionData
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}

	now := time.Now()
	for _, sd := range saved {
		if now.Before(sd.ExpiresAt) {
			// In hashed version, sd.Token is actually the hash
			a.sessions[sd.Token] = &session{
				username:  sd.Username,
				csrfToken: sd.CSRFToken,
				createdAt: sd.CreatedAt,
				expiresAt: sd.ExpiresAt,
			}
		}
	}

	return nil
}

// SaveSessions writes active sessions to disk.
func (a *AuthManager) SaveSessions() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var toSave []sessionData
	now := time.Now()
	for hashedToken, sess := range a.sessions {
		if now.Before(sess.expiresAt) {
			toSave = append(toSave, sessionData{
				Token:     hashedToken,
				Username:  sess.username,
				CSRFToken: sess.csrfToken,
				CreatedAt: sess.createdAt,
				ExpiresAt: sess.expiresAt,
			})
		}
	}

	data, err := json.Marshal(toSave)
	if err != nil {
		return err
	}

	path := filepath.Join(a.storageDir, "sessions.json")
	return os.WriteFile(path, data, 0600)
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ValidateOrigin checks if the request's Origin or Referer header matches the host,
// or appears in the configured Security.AllowedOrigins list.
// This is a defense-in-depth measure against CSRF for state-modifying requests.
func (a *AuthManager) ValidateOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}

	if origin == "" {
		return false
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	if strings.EqualFold(u.Host, r.Host) {
		return true
	}

	// Accept origins explicitly allow-listed for cross-origin access.
	originScheme := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
	for _, allowed := range a.security.AllowedOrigins {
		if strings.EqualFold(originScheme, allowed) {
			return true
		}
	}
	return false
}

// CSRFMiddleware enforces origin validation and token matching for state-modifying requests.
func (a *AuthManager) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			// 1. Origin/Referer Validation (Defense in Depth)
			if a.security.OriginValidation && !a.ValidateOrigin(r) {
				http.Error(w, `{"error":"invalid origin"}`, http.StatusForbidden)
				return
			}

			// 2. Synchronizer Token Validation (Only if Auth is enabled and request is authenticated)
			if a.cfg.Enabled {
				cookie, err := r.Cookie("kula_session")
				if err == nil {
					// We verify if this session requires CSRF token matching.
					// We only enforce the token check if it's a valid session.
					if a.ValidateSession(cookie.Value) {
						expectedToken := a.GetCSRFToken(cookie.Value)
						providedToken := r.Header.Get("X-CSRF-Token")
						if expectedToken == "" || subtle.ConstantTimeCompare([]byte(expectedToken), []byte(providedToken)) != 1 {
							http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
							return
						}
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// PrintHashedPassword generates and prints a hash for a password using the given Argon2 parameters.
func PrintHashedPassword(password string, params config.Argon2Config) {
	salt, err := GenerateSalt()
	if err != nil {
		fmt.Printf("Error generating salt: %v\n", err)
		return
	}

	hash := HashPassword(password, salt, params)
	fmt.Printf("Password hash algorithm: Argon2id\n")
	fmt.Printf("Time: %d, Memory: %d KB, Threads: %d\n", params.Time, params.Memory, params.Threads)
	fmt.Printf("Password hash: %s\n", hash)
	fmt.Printf("Salt: %s\n", salt)
	fmt.Println("\nAdd these to your config.yaml under web.auth:")
	fmt.Printf("  password_hash: \"%s\"\n", hash)
	fmt.Printf("  password_salt: \"%s\"\n", salt)
	fmt.Printf("  argon2:\n")
	fmt.Printf("    time: %d\n", params.Time)
	fmt.Printf("    memory: %d\n", params.Memory)
	fmt.Printf("    threads: %d\n", params.Threads)
}
