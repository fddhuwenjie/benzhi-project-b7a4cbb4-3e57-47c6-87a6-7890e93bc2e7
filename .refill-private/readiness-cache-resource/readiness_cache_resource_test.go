package readiness_cache_resource_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/httpapi"
	"caption-release-workbench/internal/store"
)

func TestReadinessCacheResourceInvalidation(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "readiness.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	service := application.New(repo)
	server := httpapi.New(service, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	server.Register(mux)

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("initial readiness status = %d, want 200", first.Code)
	}

	if err := repo.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
	}
	if err := repo.Ready(context.Background()); err == nil {
		t.Fatal("closed SQLite unexpectedly reports ready")
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	if second.Code == http.StatusOK {
		t.Fatal("closed repository readiness returned HTTP 200")
	}
}
