package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/christopher/restake-yield-ea/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSecurityHeaders verifies that every response carries the defensive
// security headers regardless of which handler runs.
func TestSecurityHeaders(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

// TestRequestIDGenerated verifies that a fresh X-Request-ID is generated and
// returned when the client does not supply one.
func TestRequestIDGenerated(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	rid := rec.Header().Get("X-Request-ID")
	require.NotEmpty(t, rid, "X-Request-ID must be generated")
	assert.Len(t, rid, 32, "generated ID should be 16 bytes hex = 32 chars")
}

// TestRequestIDEchoed verifies that a client-supplied X-Request-ID is honoured
// and echoed back in the response header.
func TestRequestIDEchoed(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", "client-trace-123")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	assert.Equal(t, "client-trace-123", rec.Header().Get("X-Request-ID"))
}

// TestRequestIDInResponseMeta verifies that the request ID is propagated into
// the EA response meta block for end-to-end traceability.
func TestRequestIDInResponseMeta(t *testing.T) {
	now := time.Now().Unix()
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{
			{Provider: "p1", APY: 0.04, TVL: 1000, PointsPerETH: 1.0, CollectedAt: now},
		}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	body := `{"jobRunId":"42","data":{}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Request-ID", "trace-abc")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp ChainlinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	meta, ok := resp.Data["meta"].(map[string]interface{})
	require.True(t, ok, "meta must be present")
	assert.Equal(t, "trace-abc", meta["requestId"])
}

// TestRequestBodySizeLimit verifies that an oversized request body is rejected
// before reaching the handler logic. http.MaxBytesReader causes the JSON
// decoder to return an error which maps to 400 Bad Request.
func TestRequestBodySizeLimit(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	// Build a body larger than maxRequestBodyBytes (1 MiB).
	big := strings.Repeat("x", maxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	// The oversized body causes json.Decode to fail -> 400. The key assertion
	// is that the body is not fully consumed into an unbounded buffer.
	assert.True(t, rec.Code >= 400, "oversized body must be rejected, got %d", rec.Code)
}

// TestReadyzReady verifies the readiness probe returns 200 when providers exist.
func TestReadyzReady(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: true, MinProviders: 1})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "ready")
	assert.Contains(t, body, "circuit_state")
}

// TestReadyzNotReady verifies the readiness probe returns 503 when no providers
// are configured.
func TestReadyzNotReady(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})
	srv.providers = nil // simulate no providers

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "not ready")
}

// TestRequestIDContext verifies the request ID is available on the context
// inside a downstream handler.
func TestRequestIDContext(t *testing.T) {
	var ctxID string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = requestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{})
	wrapped := srv.withMiddleware(h)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "ctx-test-id")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, "ctx-test-id", ctxID)
}

// TestNewRequestIDUniqueness verifies that generated IDs are unique.
func TestNewRequestIDUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := newRequestID()
		assert.Len(t, id, 32)
		_, dup := seen[id]
		assert.False(t, dup, "duplicate ID generated at iteration %d", i)
		seen[id] = struct{}{}
	}
}

// TestClientIP verifies X-Forwarded-For extraction and port stripping.
// Without a trusted proxy configured, XFF is never trusted.
func TestClientIP(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"direct", "192.168.1.1:54321", "", "192.168.1.1"},
		// Without trusted proxy, XFF is ignored — uses RemoteAddr.
		{"xff ignored without trusted proxy", "10.0.0.1:80", "203.0.113.5", "10.0.0.1"},
		{"no port", "192.168.1.1", "", "192.168.1.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			assert.Equal(t, tt.want, srv.clientIP(r))
		})
	}
}

// TestClientIPTrustedProxy verifies that X-Forwarded-For is honoured only
// when the request comes from the configured trusted proxy IP.
func TestClientIPTrustedProxy(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})
	srv.trustedProxy = "10.0.0.1"

	// Request from the trusted proxy — XFF should be used.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:80"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	assert.Equal(t, "203.0.113.5", srv.clientIP(r))

	// Request from the trusted proxy — multi-hop XFF, first hop used.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:80"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 70.0.0.1")
	assert.Equal(t, "203.0.113.5", srv.clientIP(r))

	// Request from a non-proxy IP — XFF must be ignored.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.99:12345"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	assert.Equal(t, "192.168.1.99", srv.clientIP(r), "XFF must be ignored from non-proxy")
}

// TestRequestIDRejectsOversized verifies that an overly long client-supplied
// X-Request-ID is replaced with a fresh generated one (prevents log injection
// / header bloat).
func TestRequestIDRejectsOversized(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", strings.Repeat("A", 200)) // > 64 chars
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	rid := rec.Header().Get("X-Request-ID")
	assert.Len(t, rid, 32, "oversized client ID should be replaced with generated 32-char ID")
}

// Ensure context import is used (requestIDFromContext takes context.Context).
var _ = context.Background

// TestAdminAuth_NoToken_AllowsAccess verifies that when ADMIN_TOKEN is not
// set, admin endpoints are accessible without authentication.
func TestAdminAuth_NoToken_AllowsAccess(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})
	srv.adminToken = ""

	h := srv.adminAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "no token set → access allowed")
}

// TestAdminAuth_ValidToken_AllowsAccess verifies that a correct bearer
// token grants access.
func TestAdminAuth_ValidToken_AllowsAccess(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})
	srv.adminToken = "secret-token"

	h := srv.adminAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "valid token → access allowed")
}

// TestAdminAuth_InvalidToken_Rejects verifies that a wrong bearer token
// is rejected with 401.
func TestAdminAuth_InvalidToken_Rejects(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})
	srv.adminToken = "secret-token"

	h := srv.adminAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "invalid token → 401")
}

// TestAdminAuth_MissingHeader_Rejects verifies that a missing
// Authorization header is rejected with 401.
func TestAdminAuth_MissingHeader_Rejects(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})
	srv.adminToken = "secret-token"

	h := srv.adminAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "missing header → 401")
}

// TestAdminAuth_WrongScheme_Rejects verifies that a non-Bearer scheme
// is rejected.
func TestAdminAuth_WrongScheme_Rejects(t *testing.T) {
	srv := newTestServer(t, []provider.Provider{
		&mockProvider{name: "p1", metrics: []model.Metric{{Provider: "p1", APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}},
	}, ServerConfig{EnableCircuitBreaker: false, EnableValidation: false})
	srv.adminToken = "secret-token"

	h := srv.adminAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Basic secret-token")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "wrong scheme → 401")
}
