package git

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// generateTestPEM creates an RSA private key in PKCS#1 PEM format for testing.
func generateTestPEM(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return pemBytes, key
}

// generateTestPEMPKCS8 creates an RSA private key in PKCS#8 PEM format.
func generateTestPEMPKCS8(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling PKCS#8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})
	return pemBytes, key
}

func TestExchangeGitHubAppToken(t *testing.T) {
	pemBytes, key := generateTestPEM(t)
	expiry := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)

	var receivedAppID string
	var receivedInstallID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing Accept header")
		}

		// Parse and validate JWT from Authorization header
		authHeader := r.Header.Get("Authorization")
		if len(authHeader) < 8 || authHeader[:7] != "Bearer " {
			t.Errorf("invalid Authorization header: %s", authHeader)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		jwtStr := authHeader[7:]

		token, err := jwt.Parse(jwtStr, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return &key.PublicKey, nil
		})
		if err != nil {
			t.Errorf("parsing JWT: %v", err)
			http.Error(w, "invalid jwt", http.StatusUnauthorized)
			return
		}

		issuer, _ := token.Claims.GetIssuer()
		receivedAppID = issuer

		// Extract installation ID from URL path
		_, _ = fmt.Sscanf(r.URL.Path, "/app/installations/%s/access_tokens", &receivedInstallID)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_test_installation_token_123",
			"expires_at": expiry.Format(time.RFC3339),
		})
	}))
	defer server.Close()

	result, err := ExchangeGitHubAppToken(context.Background(), pemBytes, 12345, 67890, server.URL)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}

	if result.Token != "ghs_test_installation_token_123" {
		t.Errorf("expected token ghs_test_installation_token_123, got %s", result.Token)
	}
	if !result.ExpiresAt.Equal(expiry) {
		t.Errorf("expected expiry %v, got %v", expiry, result.ExpiresAt)
	}
	if receivedAppID != "12345" {
		t.Errorf("expected app ID 12345 in JWT issuer, got %s", receivedAppID)
	}
}

func TestExchangeGitHubAppToken_APIError(t *testing.T) {
	pemBytes, _ := generateTestPEM(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer server.Close()

	_, err := ExchangeGitHubAppToken(context.Background(), pemBytes, 12345, 67890, server.URL)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if got := err.Error(); !contains(got, "401") {
		t.Errorf("expected error to mention 401, got: %s", got)
	}
}

func TestExchangeGitHubAppToken_DefaultAPIURL(t *testing.T) {
	pemBytes, _ := generateTestPEM(t)

	// Exchange with empty apiURL should use defaultGitHubAPIURL.
	// This will fail (no real GitHub), but we can verify it doesn't panic.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := ExchangeGitHubAppToken(ctx, pemBytes, 12345, 67890, "")
	if err == nil {
		t.Fatal("expected error when hitting real GitHub API without valid credentials")
	}
}

func TestParseRSAPrivateKey_PKCS1(t *testing.T) {
	pemBytes, expected := generateTestPEM(t)
	key, err := ParseRSAPrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("parsing PKCS#1: %v", err)
	}
	if key.D.Cmp(expected.D) != 0 {
		t.Error("parsed key does not match original")
	}
}

func TestParseRSAPrivateKey_PKCS8(t *testing.T) {
	pemBytes, expected := generateTestPEMPKCS8(t)
	key, err := ParseRSAPrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("parsing PKCS#8: %v", err)
	}
	if key.D.Cmp(expected.D) != 0 {
		t.Error("parsed key does not match original")
	}
}

func TestParseRSAPrivateKey_InvalidPEM(t *testing.T) {
	_, err := ParseRSAPrivateKey([]byte("not a pem"))
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestParseRSAPrivateKey_UnsupportedType(t *testing.T) {
	block := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: []byte("fake"),
	})
	_, err := ParseRSAPrivateKey(block)
	if err == nil {
		t.Fatal("expected error for unsupported PEM type")
	}
	if got := err.Error(); !contains(got, "unsupported PEM block type") {
		t.Errorf("expected unsupported type error, got: %s", got)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
