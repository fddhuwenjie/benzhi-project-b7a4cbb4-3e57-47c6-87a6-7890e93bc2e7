package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/store"
)

func TestProjectRoutesAndStructuredError(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	mux := http.NewServeMux()
	New(application.New(repo), slog.Default()).Register(mux)
	body := map[string]any{"request_id": "req-1", "id": "p1", "title": "节目", "duration_ms": 10000, "language": "zh-CN", "media_checksum": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "style_profile": "规范", "assignee": "甲", "actor": "甲"}
	response := performJSON(t, mux, http.MethodPost, "/api/projects", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("创建状态 %d: %s", response.Code, response.Body.String())
	}
	response = performJSON(t, mux, http.MethodPost, "/api/projects", body)
	if response.Code != http.StatusOK || response.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("幂等响应无效: %d", response.Code)
	}
	duplicate := map[string]any{"request_id": "req-duplicate", "id": "p2", "title": "另一项目", "duration_ms": 10000, "language": "zh-CN", "media_checksum": "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF", "style_profile": "规范", "assignee": "乙", "actor": "乙"}
	response = performJSON(t, mux, http.MethodPost, "/api/projects", duplicate)
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"project_id":"p1"`)) {
		t.Fatalf("重复素材响应无效: %d %s", response.Code, response.Body.String())
	}
	badSave := map[string]any{"request_id": "req-2", "expected_revision": 99, "actor": "甲", "cues": []any{}}
	response = performJSON(t, mux, http.MethodPut, "/api/projects/p1/cues", badSave)
	if response.Code != http.StatusConflict {
		t.Fatalf("预期修订冲突，得到 %d", response.Code)
	}
	var errorEnvelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &errorEnvelope); err != nil || errorEnvelope.Error.Code != "conflict" || errorEnvelope.Error.Message == "" {
		t.Fatalf("错误结构无效: %s", response.Body.String())
	}
	response = performJSON(t, mux, http.MethodGet, "/api/projects/p1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("项目查询失败: %d", response.Code)
	}
}

func performJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
