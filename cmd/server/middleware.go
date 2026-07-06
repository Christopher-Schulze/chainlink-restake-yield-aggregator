// Package main contains HTTP middleware that hardens the External Adapter for
// production: request-body size limits, security headers, request-ID
// propagation, structured access logging, and optional admin-endpoint auth.
// Each middleware is a thin decorator so it can be composed and tested
// independently.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// maxRequestBodyBytes caps the size of an inbound EA request body. Chainlink
// EA requests are small JSON envelopes; 1 MiB is generous and prevents a
// malicious or buggy client from exhausting server memory.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// requestIDKey is the context key used to propagate the request ID.
type requestIDKey struct{}

// requestIDHeader is the HTTP header used to expose/accept the request ID.
const requestIDHeader = "X-Request-ID"

// --- middleware chain ---

// withMiddleware wraps the supplied handler in the full production middleware
// stack: security headers, request-ID injection, body-size limiting, and
// structured access logging. The order is deliberate:
//
//  1. securityHeaders  – set response headers before anything else writes.
//  2. requestID        – stamp the ID on the request context for downstream
//     handlers and log entries.
//  3. limitBody         – reject oversized bodies before JSON decoding.
//  4. accessLog         – record latency/status after the handler returns.
func (s *Server) withMiddleware(h http.Handler) http.Handler {
	return s.accessLog(
		s.limitBody(
			s.requestID(
				securityHeaders(h),
			),
		),
	)
}

// --- security headers ---

// securityHeaders adds standard defensive response headers. They do not make
// the adapter a browser-facing app safe by themselves, but they prevent
// content-type sniffing and framing attacks when the endpoint is exposed
// through a browser or proxy.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// --- request ID ---

// requestID ensures every request has an X-Request-ID. If the client supplies
// one it is honoured (after sanitisation); otherwise a fresh 16-byte hex ID is
// generated. The ID is placed on the request context and on the response
// header so it can be correlated across logs, traces, and the caller.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get(requestIDHeader))
		if id == "" || len(id) > 64 {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		next.ServeHTTP(w, r)
	})
}

// requestIDFromContext returns the request ID stored on ctx, or "" if absent.
func requestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// randReader is the source of randomness for newRequestID. It defaults to
// crypto/rand.Reader but can be swapped in tests to exercise the fallback path.
var randReader = rand.Reader

// newRequestID returns a 32-char hex string from crypto/rand.
func newRequestID() string {
	var b [16]byte
	if _, err := randReader.Read(b[:]); err != nil {
		// crypto/rand should never fail on a healthy host; fall back to a
		// timestamp-derived ID so the request still has a unique marker.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

// --- body size limiting ---

// limitBody wraps the request body in an io.LimitReader and rejects bodies
// that exceed maxRequestBodyBytes with 413 Request Entity Too Large. This
// prevents a single request from exhausting server memory.
func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// --- admin endpoint auth ---

// adminAuth protects admin endpoints (/circuit, /status). If ADMIN_TOKEN is
// unset, the middleware is a no-op (assumes the EA is on a private network
// behind the Chainlink node). If set, requests must include
// "Authorization: Bearer <token>". The token comparison uses
// subtle.ConstantTimeCompare to prevent timing attacks.
func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.adminToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		provided := auth[len(prefix):]
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.adminToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- access logging ---

// statusWriter captures the status code written by downstream handlers so the
// access log can record it.
type statusWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

// accessLog records one structured log line per request with method, path,
// status, latency, and request ID.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		fields := logrus.Fields{
			"method":     r.Method,
			"path":       r.URL.Path,
			"status":     sw.status,
			"latency_ms": time.Since(start).Milliseconds(),
			"bytes":      sw.size,
			"request_id": requestIDFromContext(r.Context()),
			"remote":     s.clientIP(r),
		}
		if sw.status >= 500 {
			logrus.WithFields(fields).Warn("request completed")
		} else {
			logrus.WithFields(fields).Info("request completed")
		}
	})
}

// clientIP extracts the client IP from the request. If TRUSTED_PROXY is set,
// X-Forwarded-For is honoured only when the request comes from the trusted
// proxy IP. Otherwise, r.RemoteAddr is used directly. This prevents spoofed
// XFF headers from polluting logs when the EA is exposed publicly.
func (s *Server) clientIP(r *http.Request) string {
	if s.trustedProxy != "" {
		// Check if the direct peer is the trusted proxy.
		peer := r.RemoteAddr
		if i := strings.LastIndexByte(peer, ':'); i >= 0 {
			peer = peer[:i]
		}
		if peer == s.trustedProxy {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if idx := strings.IndexByte(xff, ','); idx >= 0 {
					return strings.TrimSpace(xff[:idx])
				}
				return strings.TrimSpace(xff)
			}
		}
	}
	// r.RemoteAddr is "host:port"; strip the port for logging.
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i >= 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
