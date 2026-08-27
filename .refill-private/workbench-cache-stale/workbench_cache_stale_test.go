package workbench_cache_stale_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
	"caption-release-workbench/internal/httpapi"
	"caption-release-workbench/internal/store"
)

func TestWorkbenchCacheInvalidation(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "workbench-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	mux := http.NewServeMux()
	httpapi.New(application.New(repo), slog.Default()).Register(mux)

	created := performJSON(t, mux, http.MethodPost, "/api/projects", map[string]any{
		"request_id": "create-workbench-cache", "id": "cache-project", "title": "缓存核验节目",
		"duration_ms": 10000, "language": "zh-CN",
		"media_checksum": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		"style_profile":  "公共广播规范", "assignee": "制作员甲", "actor": "制作员甲",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("创建项目失败: status=%d body=%s", created.Code, created.Body.String())
	}

	first := performJSON(t, mux, http.MethodGet, "/api/projects/cache-project/workbench", nil)
	firstView := decodeWorkbench(t, first)
	if firstView.Project.Revision != 1 || len(firstView.Project.Cues) != 0 {
		t.Fatalf("初始工作台异常: revision=%d cues=%d", firstView.Project.Revision, len(firstView.Project.Cues))
	}

	saved := performJSON(t, mux, http.MethodPut, "/api/projects/cache-project/cues", map[string]any{
		"request_id": "save-after-workbench-cache", "expected_revision": 1, "actor": "制作员甲",
		"cues": []map[string]any{{
			"id": "cue-1", "start_ms": 0, "end_ms": 2500, "speaker": "主播", "text": "这是更新后的字幕。",
		}},
	})
	if saved.Code != http.StatusOK {
		t.Fatalf("保存字幕失败: status=%d body=%s", saved.Code, saved.Body.String())
	}

	projectResponse := performJSON(t, mux, http.MethodGet, "/api/projects/cache-project", nil)
	var current domain.CaptionProject
	decodeJSON(t, projectResponse, &current)
	if current.Revision != 2 || len(current.Cues) != 1 {
		t.Fatalf("存储未提交预期状态: revision=%d cues=%d", current.Revision, len(current.Cues))
	}

	second := performJSON(t, mux, http.MethodGet, "/api/projects/cache-project/workbench", nil)
	secondView := decodeWorkbench(t, second)
	if secondView.Project.Revision != current.Revision || len(secondView.Project.Cues) != len(current.Cues) {
		t.Fatalf("工作台缓存未随成功写事务失效: workbench_revision=%d stored_revision=%d workbench_cues=%d stored_cues=%d",
			secondView.Project.Revision, current.Revision, len(secondView.Project.Cues), len(current.Cues))
	}
}

func decodeWorkbench(t *testing.T, response *httptest.ResponseRecorder) application.WorkbenchView {
	t.Helper()
	var view application.WorkbenchView
	decodeJSON(t, response, &view)
	return view
}

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP 请求失败: status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("解析响应失败: %v body=%s", err, response.Body.String())
	}
}

func performJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
