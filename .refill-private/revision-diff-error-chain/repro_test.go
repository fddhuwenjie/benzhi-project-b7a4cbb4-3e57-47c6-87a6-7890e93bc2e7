package revisiondifferrorchain

import (
	"context"
	"errors"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type gatedRepository struct {
	domain.Repository
	started chan struct{}
	release chan struct{}
}

func (r *gatedRepository) Get(context.Context, string) (*domain.CaptionProject, error) {
	return &domain.CaptionProject{ID: "project-1", Status: domain.StatusDraft, Revision: 2}, nil
}

func (r *gatedRepository) RevisionCues(ctx context.Context, projectID string, revision int64) ([]domain.CaptionCue, string, error) {
	if revision == 1 {
		close(r.started)
		<-r.release
		return []domain.CaptionCue{{ID: "cue-1", ProjectID: projectID, StartMS: 0, EndMS: 1000, Text: "旧字幕", CueRevision: 1}}, "old", nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return []domain.CaptionCue{{ID: "cue-1", ProjectID: projectID, StartMS: 0, EndMS: 1000, Text: "新字幕", CueRevision: 2}}, "new", nil
}

func TestRevisionDiffPreservesSnapshotCancellation(t *testing.T) {
	repo := &gatedRepository{started: make(chan struct{}), release: make(chan struct{})}
	service := application.New(repo)
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		_, err := service.RevisionDiff(ctx, "project-1", 1, 2)
		resultCh <- err
	}()
	<-repo.started
	cancel()
	close(repo.release)
	err := <-resultCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("修订差异应保留 context.Canceled，实际错误: %v", err)
	}
}
