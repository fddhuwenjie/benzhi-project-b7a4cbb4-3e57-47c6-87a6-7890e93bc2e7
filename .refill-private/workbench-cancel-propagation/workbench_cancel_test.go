package workbench_cancel_propagation_test

import (
	"context"
	"errors"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type cancellationRepository struct {
	domain.Repository
	project          *domain.CaptionProject
	manifestRead     chan struct{}
	continueManifest chan struct{}
	getCalls         int
}

func (r *cancellationRepository) Get(ctx context.Context, _ string) (*domain.CaptionProject, error) {
	r.getCalls++
	if r.getCalls == 1 {
		return r.project, nil
	}
	close(r.manifestRead)
	<-r.continueManifest
	return nil, ctx.Err()
}

func (r *cancellationRepository) Audit(context.Context, string, int64, int) ([]domain.AuditEvent, error) {
	return []domain.AuditEvent{}, nil
}

func TestWorkbenchCancellationPropagation(t *testing.T) {
	repo := &cancellationRepository{
		project:          &domain.CaptionProject{ID: "released-project", Status: domain.StatusReleased, Revision: 7, DurationMS: 60_000},
		manifestRead:     make(chan struct{}),
		continueManifest: make(chan struct{}),
	}
	service := application.New(repo)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	go func() {
		_, err := service.GetWorkbench(ctx, repo.project.ID)
		result <- err
	}()

	<-repo.manifestRead
	cancel()
	close(repo.continueManifest)

	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("TestWorkbenchCancellationPropagation: 工作台清单读取应传播 context.Canceled，实际错误为 %v", err)
	}
}
