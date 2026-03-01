package web

import (
	"kula-szpiegula/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHashPasswordDeterminism(t *testing.T) {
	hash1 := HashPassword("testpass", "salt123")
	hash2 := HashPassword("testpass", "salt123")
	if hash1 != hash2 {
		t.Errorf("HashPassword not deterministic: %q != %q", hash1, hash2)
	}
}

func TestHashPasswordDifferentSalts(t *testing.T) {
	hash1 := HashPassword("testpass", "salt1")
	hash2 := HashPassword("testpass", "salt2")
	if hash1 == hash2 {
		t.Error("Same password with different salts should produce different hashes")
	}
}

func TestHashPasswordDifferentPasswords(t *testing.T) {
	hash1 := HashPassword("pass1", "same_salt")
	hash2 := HashPassword("pass2", "same_salt")
	if hash1 == hash2 {
		t.Error("Different passwords with same salt should produce different hashes")
	}
}

func TestHashPasswordLength(t *testing.T) {
	hash := HashPassword("test", "salt")
	// Argon2id produces 256-bit hash = 32 bytes = 64 hex chars based on keyLen=32
	if len(hash) != 64 {
		t.Errorf("Hash length = %d, want 64 hex chars (Argon2id 256-bit)", len(hash))
	}
}

func TestGenerateSalt(t *testing.T) {
	salt1, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt() error: %v", err)
	}
	salt2, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt() error: %v", err)
	}
	if salt1 == salt2 {
		t.Error("Two GenerateSalt() calls should produce different values")
	}
	// 32 bytes = 64 hex chars
	if len(salt1) != 64 {
		t.Errorf("Salt length = %d, want 64 hex chars", len(salt1))
	}
}

func TestValidateCredentialsDisabled(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{Enabled: false})
	if !am.ValidateCredentials("any", "any") {
		t.Error("With auth disabled, ValidateCredentials should return true")
	}
}

func TestValidateCredentialsCorrect(t *testing.T) {
	salt, _ := GenerateSalt()
	hash := HashPassword("secret", salt)
	am := NewAuthManager(config.AuthConfig{
		Enabled:      true,
		Username:     "admin",
		PasswordHash: hash,
		PasswordSalt: salt,
	})
	if !am.ValidateCredentials("admin", "secret") {
		t.Error("Valid credentials should pass")
	}
}

func TestValidateCredentialsWrong(t *testing.T) {
	salt, _ := GenerateSalt()
	hash := HashPassword("secret", salt)
	am := NewAuthManager(config.AuthConfig{
		Enabled:      true,
		Username:     "admin",
		PasswordHash: hash,
		PasswordSalt: salt,
	})
	if am.ValidateCredentials("admin", "wrong") {
		t.Error("Wrong password should fail")
	}
	if am.ValidateCredentials("wrong", "secret") {
		t.Error("Wrong username should fail")
	}
}

func TestSessionLifecycle(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{
		Enabled:        true,
		SessionTimeout: time.Hour,
	})

	token, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	if token == "" {
		t.Fatal("CreateSession returned empty token")
	}
	if !am.ValidateSession(token) {
		t.Error("Newly created session should be valid")
	}
	if am.ValidateSession("invalid_token") {
		t.Error("Invalid token should not validate")
	}
}

func TestSessionExpiry(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{
		Enabled:        true,
		SessionTimeout: time.Millisecond, // very short timeout
	})

	token, _ := am.CreateSession("admin")
	time.Sleep(5 * time.Millisecond)
	if am.ValidateSession(token) {
		t.Error("Expired session should not validate")
	}
}

func TestCleanupSessions(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{
		Enabled:        true,
		SessionTimeout: time.Millisecond,
	})

	_, _ = am.CreateSession("user1")
	_, _ = am.CreateSession("user2")
	time.Sleep(5 * time.Millisecond)
	am.CleanupSessions()

	am.mu.RLock()
	count := len(am.sessions)
	am.mu.RUnlock()

	if count != 0 {
		t.Errorf("After cleanup, sessions count = %d, want 0", count)
	}
}

func TestAuthMiddlewareDisabled(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{Enabled: false})
	handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Auth disabled: status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareNoToken(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{
		Enabled:        true,
		SessionTimeout: time.Hour,
	})
	handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("No token: status = %d, want 401", rec.Code)
	}
}

func TestAuthMiddlewareValidCookie(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{
		Enabled:        true,
		SessionTimeout: time.Hour,
	})
	token, _ := am.CreateSession("admin")

	handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "kula_session", Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Valid cookie: status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareBearerToken(t *testing.T) {
	am := NewAuthManager(config.AuthConfig{
		Enabled:        true,
		SessionTimeout: time.Hour,
	})
	token, _ := am.CreateSession("admin")

	handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Bearer token: status = %d, want 200", rec.Code)
	}
}
