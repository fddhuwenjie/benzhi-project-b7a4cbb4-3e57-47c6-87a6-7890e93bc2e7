package manifestresourceownership

import (
	"context"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type mismatchedManifestRepository struct {
	domain.Repository
}

func (mismatchedManifestRepository) Get(context.Context, string) (*domain.CaptionProject, error) {
	return &domain.CaptionProject{
		ID:       "project-1",
		Status:   domain.StatusReleased,
		Revision: 4,
		Cues:     []domain.CaptionCue{},
	}, nil
}

func (mismatchedManifestRepository) Manifest(context.Context, string) (*domain.ReleaseManifest, error) {
	return &domain.ReleaseManifest{
		ID:              "manifest-1",
		ProjectID:       "project-from-another-request",
		ProjectRevision: 4,
		ManifestVersion: "1",
	}, nil
}

func TestVerificationPackageRejectsMismatchedManifest(t *testing.T) {
	service := application.New(mismatchedManifestRepository{})
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("发布清单资源归属异常不应导致核验包构造崩溃: %v", recovered)
		}
	}()

	_, err := service.VerificationPackage(context.Background(), "project-1")
	if err == nil {
		t.Fatal("资源归属校验失败时应返回可观察错误")
	}
}
