package verification_cache_alias_test

import (
	"context"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

func TestVerificationPackageCacheIsolation(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	project := &domain.CaptionProject{
		ID:            "project-cache-isolation",
		Title:         "晚间新闻",
		DurationMS:    10_000,
		Language:      "zh-CN",
		MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StyleProfile:  "公共广播字幕规范",
		Assignee:      "制作员甲",
		Status:        domain.StatusReleased,
		Revision:      8,
		CreatedAt:     now.Add(-time.Hour),
		UpdatedAt:     now,
		Cues: []domain.CaptionCue{{
			ID: "cue-1", ProjectID: "project-cache-isolation", Sequence: 1,
			StartMS: 0, EndMS: 2_000, Speaker: "主播", Text: "原始冻结字幕", CueRevision: 3,
		}},
	}
	checksum := project.CaptionChecksum()
	manifest := &domain.ReleaseManifest{
		ID: "manifest-1", ProjectID: project.ID, ProjectRevision: project.Revision,
		CueCount: len(project.Cues), CaptionChecksum: checksum, MediaChecksum: project.MediaChecksum,
		ApprovedBy: "发布负责人", ApprovedAt: now, ManifestVersion: "1",
	}
	project.Manifest = manifest
	repo := &verificationRepository{
		project:  project,
		manifest: manifest,
		approval: domain.AuditEvent{
			ID: 41, ProjectID: project.ID, Type: "release.approved", Actor: manifest.ApprovedBy,
			Revision: project.Revision, CreatedAt: manifest.ApprovedAt,
		},
	}
	service := application.New(repo)

	first, err := service.VerificationPackage(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("首次生成核验包失败: %v", err)
	}
	first.Captions[0].Text = "调用方临时标记"

	second, err := service.VerificationPackage(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("再次生成核验包失败: %v", err)
	}
	if second.Captions[0].Text != "原始冻结字幕" {
		t.Fatalf("缓存返回值发生跨请求污染: got %q", second.Captions[0].Text)
	}
}

type verificationRepository struct {
	project  *domain.CaptionProject
	manifest *domain.ReleaseManifest
	approval domain.AuditEvent
}

func (r *verificationRepository) Get(context.Context, string) (*domain.CaptionProject, error) {
	copyProject := *r.project
	copyProject.Cues = append([]domain.CaptionCue(nil), r.project.Cues...)
	return &copyProject, nil
}
func (r *verificationRepository) Manifest(context.Context, string) (*domain.ReleaseManifest, error) {
	copyManifest := *r.manifest
	return &copyManifest, nil
}
func (r *verificationRepository) RevisionCues(context.Context, string, int64) ([]domain.CaptionCue, string, error) {
	cues := append([]domain.CaptionCue(nil), r.project.Cues...)
	return cues, r.project.CaptionChecksum(), nil
}
func (r *verificationRepository) AuditQuery(context.Context, string, domain.AuditQuery) (domain.AuditPage, error) {
	return domain.AuditPage{Events: []domain.AuditEvent{r.approval}}, nil
}
func (r *verificationRepository) Create(context.Context, *domain.CaptionProject, string, string) (*domain.MutationResult, bool, error) {
	panic("unexpected Create")
}
func (r *verificationRepository) FindByMediaChecksum(context.Context, string) (*domain.ProjectSummary, error) {
	panic("unexpected FindByMediaChecksum")
}
func (r *verificationRepository) Mutate(context.Context, domain.Mutation, func(*domain.CaptionProject) (any, error)) (*domain.MutationResult, bool, error) {
	panic("unexpected Mutate")
}
func (r *verificationRepository) List(context.Context) ([]domain.ProjectSummary, error) {
	panic("unexpected List")
}
func (r *verificationRepository) ListFiltered(context.Context, domain.QueueFilter) ([]domain.ProjectSummary, domain.QueueStats, error) {
	panic("unexpected ListFiltered")
}
func (r *verificationRepository) Audit(context.Context, string, int64, int) ([]domain.AuditEvent, error) {
	panic("unexpected Audit")
}
func (r *verificationRepository) AuditEvent(context.Context, string, int64) (*domain.AuditEvent, error) {
	panic("unexpected AuditEvent")
}
func (r *verificationRepository) CueAtRevision(context.Context, string, string, int64) (*domain.CaptionCue, error) {
	panic("unexpected CueAtRevision")
}
func (r *verificationRepository) Ready(context.Context) error { return nil }
func (r *verificationRepository) Close() error                { return nil }
