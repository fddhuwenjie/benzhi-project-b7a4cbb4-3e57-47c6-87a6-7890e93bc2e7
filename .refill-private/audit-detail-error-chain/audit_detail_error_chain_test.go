package auditdetailerrorchain_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/httpapi"
	"caption-release-workbench/internal/store"
)

func TestAuditEventErrorClassification(t *testing.T) {
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开测试存储: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	now := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	service := application.NewWithDependencies(repo, func() time.Time { return now }, func(prefix string) string { return prefix + "-fixed" })
	_, _, err = service.CreateProject(context.Background(), application.CreateProjectCommand{
		RequestID:     "request-create-audit-chain",
		ID:            "project-audit-chain",
		Title:         "审计错误链测试项目",
		DurationMS:    60000,
		Language:      "zh-CN",
		MediaChecksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		StyleProfile:  "公共广播",
		Assignee:      "制作员",
		Actor:         "测试员",
	})
	if err != nil {
		t.Fatalf("创建测试项目: %v", err)
	}

	server := httpapi.New(service, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	server.Register(mux)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/projects/project-audit-chain/audit/999", nil)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("审计事件不存在应保留 domain.BusinessError 并返回 404，实际状态码 %d，响应 %s", recorder.Code, recorder.Body.String())
	}
}
