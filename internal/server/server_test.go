package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petebeegle/homelab-access/internal/config"
)

func TestHealthz(t *testing.T) {
	handler := New(config.Config{HTTPAddr: ":8080", DatabasePath: "/tmp/test.db"}, slog.Default())

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestReadyzReportsMissingRuntimeConfig(t *testing.T) {
	handler := New(config.Config{HTTPAddr: ":8080", DatabasePath: "/tmp/test.db"}, slog.Default())

	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, response.Code)
	}
	if !strings.Contains(response.Body.String(), "DISCORD_BOT_TOKEN") {
		t.Fatalf("expected missing token in body: %s", response.Body.String())
	}
}

func TestMetrics(t *testing.T) {
	handler := New(config.Config{HTTPAddr: ":8080", DatabasePath: "/tmp/test.db"}, slog.Default())

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	if !strings.Contains(response.Body.String(), "homelab_access_info") {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}
