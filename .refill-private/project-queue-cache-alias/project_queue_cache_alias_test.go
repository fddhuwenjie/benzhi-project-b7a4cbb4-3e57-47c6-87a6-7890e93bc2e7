package project_queue_cache_alias_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
	"caption-release-workbench/internal/store"
)

func TestProjectQueueCacheIsolation(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "queue-cache.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	now := time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)
	service := application.NewWithDependencies(repo, func() time.Time { return now }, func(prefix string) string { return prefix + "-fixed" })
	_, _, err = service.CreateProject(context.Background(), application.CreateProjectCommand{
		RequestID:     "request-create-queue-alias",
		ID:            "project-queue-alias",
		Title:         "原始节目标题",
		DurationMS:    60_000,
		Language:      "zh-CN",
		MediaChecksum: strings.Repeat("a", 64),
		StyleProfile:  "公共广播字幕规范",
		Assignee:      "字幕制作员",
		Actor:         "测试用户",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	first, err := service.ProjectQueue(context.Background(), domain.QueueFilter{})
	if err != nil {
		t.Fatalf("first queue query: %v", err)
	}
	if len(first.Projects) != 1 {
		t.Fatalf("expected one project, got %d", len(first.Projects))
	}
	first.Projects[0].Title = "调用方污染的标题"
	first.Stats.StatusCounts[domain.StatusDraft] = 999

	second, err := service.ProjectQueue(context.Background(), domain.QueueFilter{})
	if err != nil {
		t.Fatalf("second queue query: %v", err)
	}
	if second.Projects[0].Title != "原始节目标题" || second.Stats.StatusCounts[domain.StatusDraft] != 1 {
		t.Fatalf("cached queue leaked caller mutation: title=%q draft_count=%d", second.Projects[0].Title, second.Stats.StatusCounts[domain.StatusDraft])
	}
}
