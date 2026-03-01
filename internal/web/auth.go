package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"kula-szpiegula/internal/config"

	"golang.org/x/crypto/argon2"
)

// AuthManager handles authentication validation and sessions.

type AuthManager struct {
	mu       sync.RWMutex
	cfg      config.AuthConfig
	sessions map[string]*session
}

type session struct {
	username  string
	createdAt time.Time
	expiresAt time.Time
}

func NewAuthManager(cfg config.AuthConfig) *AuthManager {
	return &AuthManager{
		cfg:      cfg,
		sessions: make(map[string]*session),
	}
}

// HashPassword creates an Argon2id hash with the given salt.
func HashPassword(password, salt string) string {
	// Standard Argon2id parameters
	timeParam := uint32(1)
	memory := uint32(64 * 1024)
	threads := uint8(4)
	keyLen := uint32(32)

	hash := argon2.IDKey([]byte(password), []byte(salt), timeParam, memory, threads, keyLen)
	return hex.EncodeToString(hash)
}

// GenerateSalt creates a random 32-byte hex salt.
func GenerateSalt() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidateCredentials checks username and password against config.
func (a *AuthManager) ValidateCredentials(username, password string) bool {
	if !a.cfg.Enabled {
		return true
	}

	if subtle.ConstantTimeCompare([]byte(username), []byte(a.cfg.Username)) != 1 {
		return false
	}

	hash := HashPassword(password, a.cfg.PasswordSalt)
	return subtle.ConstantTimeCompare([]byte(hash), []byte(a.cfg.PasswordHash)) == 1
}

// CreateSession creates a new authenticated session.
func (a *AuthManager) CreateSession(username string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	token, err := generateToken()
	if err != nil {
		return "", err
	}
	a.sessions[token] = &session{
		username:  username,
		createdAt: time.Now(),
		expiresAt: time.Now().Add(a.cfg.SessionTimeout),
	}

	return token, nil
}

// ValidateSession checks if a session token is valid.
func (a *AuthManager) ValidateSession(token string) bool {
	a.mu.RLock()
	sess, ok := a.sessions[token]
	if !ok {
		a.mu.RUnlock()
		return false
	}

	if time.Now().After(sess.expiresAt) {
		a.mu.RUnlock()
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()
		return false
	}
	a.mu.RUnlock()

	return true
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

// CleanupSessions removes expired sessions periodically.
func (a *AuthManager) CleanupSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for token, sess := range a.sessions {
		if now.After(sess.expiresAt) {
			delete(a.sessions, token)
		}
	}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// PrintHashedPassword generates and prints a hash for a password.
func PrintHashedPassword(password string) {
	salt, err := GenerateSalt()
	if err != nil {
		fmt.Printf("Error generating salt: %v\n", err)
		return
	}

	hash := HashPassword(password, salt)
	fmt.Printf("Password hash: %s\n", hash)
	fmt.Printf("Salt: %s\n", salt)
	fmt.Println("\nAdd these to your config.yaml:")
	fmt.Printf("  password_hash: \"%s\"\n", hash)
	fmt.Printf("  password_salt: \"%s\"\n", salt)
}
