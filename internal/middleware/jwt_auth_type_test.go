package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJWTAuthRejectsRefreshToken(t *testing.T) {
	secret := []byte("test-secret-at-least-32-bytes-long!!")
	refresh, err := GenerateRefreshToken(secret, "u1", "uuid-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	access, err := GenerateAccessToken(secret, "u1", "user", "uuid-1", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// client is only used for blacklist lookups; nil is fine when tokens are fresh
	auth := JWTAuth(secret, nil)(okHandler)

	t.Run("refresh bearer rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/api/videos", nil)
		req.Header.Set("Authorization", "Bearer "+refresh)
		rec := httptest.NewRecorder()
		auth.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("refresh cookie alone rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/api/videos", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refresh})
		rec := httptest.NewRecorder()
		auth.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (missing access; refresh must not fall through)", rec.Code)
		}
	})
	t.Run("access bearer accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/api/videos", nil)
		req.Header.Set("Authorization", "Bearer "+access)
		rec := httptest.NewRecorder()
		auth.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
		}
	})
	t.Run("access cookie accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/api/videos", nil)
		req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refresh})
		rec := httptest.NewRecorder()
		auth.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestJWTRefreshRejectsAccessToken(t *testing.T) {
	secret := []byte("test-secret-at-least-32-bytes-long!!")
	access, err := GenerateAccessToken(secret, "u1", "user", "uuid-1", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := GenerateRefreshToken(secret, "u1", "uuid-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := JWTRefresh(secret, nil)(okHandler)

	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/token/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("access as refresh: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/api/auth/token/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refresh})
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh cookie: status = %d, want 200", rec.Code)
	}
}
