package releasepreviewcachescope_test

import (
	"context"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type projectRepository struct {
	domain.Repository
	projects map[string]*domain.CaptionProject
}

func (r *projectRepository) Get(_ context.Context, id string) (*domain.CaptionProject, error) {
	project, ok := r.projects[id]
	if !ok {
		return nil, domain.NotFound("项目", id)
	}
	copyProject := *project
	copyProject.Cues = append([]domain.CaptionCue(nil), project.Cues...)
	copyProject.Checks = append([]domain.RuleCheck(nil), project.Checks...)
	copyProject.CheckRuns = append([]domain.RuleCheckRun(nil), project.CheckRuns...)
	copyProject.Findings = append([]domain.ReviewFinding(nil), project.Findings...)
	return &copyProject, nil
}

func TestReleasePreviewCacheSeparatesProjects(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	project := func(id, media, text string) *domain.CaptionProject {
		return &domain.CaptionProject{
			ID: id, Title: id, DurationMS: 3000, Language: "zh-CN",
			MediaChecksum: media, StyleProfile: "广播字幕", Assignee: "制作员",
			Status: domain.StatusReady, Revision: 9, CreatedAt: now, UpdatedAt: now,
			Cues:      []domain.CaptionCue{{ID: id + "-cue", ProjectID: id, Sequence: 1, StartMS: 0, EndMS: 2000, Speaker: "主播", Text: text, CueRevision: 1}},
			Checks:    []domain.RuleCheck{{ID: id + "-check", Rule: "timeline", Level: "error", Passed: true, CheckedAt: now}},
			CheckRuns: []domain.RuleCheckRun{{ID: id + "-run", ProjectRevision: 9, RunAt: now}},
			Findings:  []domain.ReviewFinding{},
		}
	}
	const mediaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const mediaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	repo := &projectRepository{projects: map[string]*domain.CaptionProject{
		"project-a": project("project-a", mediaA, "甲项目字幕"),
		"project-b": project("project-b", mediaB, "乙项目字幕"),
	}}
	service := application.New(repo)

	first, err := service.ReleasePreview(context.Background(), "project-a")
	if err != nil {
		t.Fatalf("project-a 发布预览失败: %v", err)
	}
	if first.MediaChecksum != mediaA {
		t.Fatalf("project-a 素材校验值错误: %s", first.MediaChecksum)
	}

	second, err := service.ReleasePreview(context.Background(), "project-b")
	if err != nil {
		t.Fatalf("project-b 发布预览失败: %v", err)
	}
	if second.MediaChecksum != mediaB {
		t.Fatalf("project-b 复用了其他项目的发布预览: got %s want %s", second.MediaChecksum, mediaB)
	}
	if second.ConfirmationToken == first.ConfirmationToken {
		t.Fatal("不同项目不应共享发布确认令牌")
	}
}
