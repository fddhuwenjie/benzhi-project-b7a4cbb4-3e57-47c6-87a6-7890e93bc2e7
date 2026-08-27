package verification_audit_cancel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type cancelRepo struct {
	domain.Repository
	project *domain.CaptionProject
}

func (r *cancelRepo) Get(context.Context, string) (*domain.CaptionProject, error) {
	return r.project, nil
}
func (r *cancelRepo) Manifest(context.Context, string) (*domain.ReleaseManifest, error) {
	return &domain.ReleaseManifest{ID: "manifest-1", ProjectID: r.project.ID, ProjectRevision: r.project.Revision, CueCount: 0, CaptionChecksum: r.project.CaptionChecksum(), MediaChecksum: r.project.MediaChecksum, ApprovedBy: "负责人", ApprovedAt: time.Now().UTC(), ManifestVersion: "1"}, nil
}
func (r *cancelRepo) RevisionCues(context.Context, string, int64) ([]domain.CaptionCue, string, error) {
	return []domain.CaptionCue{}, r.project.CaptionChecksum(), nil
}
func (r *cancelRepo) AuditQuery(ctx context.Context, _ string, _ domain.AuditQuery) (domain.AuditPage, error) {
	return domain.AuditPage{}, ctx.Err()
}

func TestVerificationPackageAuditCancellation(t *testing.T) {
	project, err := domain.CreateProject(domain.NewProject{ID: "project-1", Title: "节目", DurationMS: 10000, Language: "zh-CN", MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", StyleProfile: "规范", Assignee: "制作员"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	project.Status = domain.StatusReleased
	service := application.New(&cancelRepo{project: project})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.VerificationPackage(ctx, project.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation to propagate, got %v", err)
	}
}
