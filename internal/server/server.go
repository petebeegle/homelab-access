package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/petebeegle/homelab-access/internal/config"
)

type Server struct {
	cfg       config.Config
	logger    *slog.Logger
	startedAt time.Time
	requests  atomic.Uint64
}

func New(cfg config.Config, logger *slog.Logger) http.Handler {
	s := &Server{
		cfg:       cfg,
		logger:    logger,
		startedAt: time.Now().UTC(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("POST /discord/interactions", s.notImplemented("discord interactions are not implemented in the foundation build"))
	mux.HandleFunc("GET /download/{token}", s.notImplemented("one-time VPN downloads are not implemented in the foundation build"))
	mux.HandleFunc("/", s.notFound)

	return s.logRequests(mux)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	missing := s.cfg.MissingRuntimeKeys()
	if len(missing) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":  "not_ready",
			"missing": missing,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("# HELP homelab_access_info Static service information.\n"))
	_, _ = w.Write([]byte("# TYPE homelab_access_info gauge\n"))
	_, _ = w.Write([]byte("homelab_access_info{version=\"foundation\"} 1\n"))
	_, _ = w.Write([]byte("# HELP homelab_access_http_requests_total HTTP requests handled by the service.\n"))
	_, _ = w.Write([]byte("# TYPE homelab_access_http_requests_total counter\n"))
	_, _ = w.Write([]byte("homelab_access_http_requests_total " + uintToString(s.requests.Load()) + "\n"))
}

func (s *Server) notImplemented(message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"status":  "not_implemented",
			"message": message,
		})
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") {
		writeJSON(w, http.StatusNotFound, map[string]string{"status": "not_found"})
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func uintToString(value uint64) string {
	if value == 0 {
		return "0"
	}

	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
