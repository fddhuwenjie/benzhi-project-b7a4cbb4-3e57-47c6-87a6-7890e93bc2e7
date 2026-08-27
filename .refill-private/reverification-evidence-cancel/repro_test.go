package reverificationevidencecancel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type cancelSnapshotRepo struct {
	domain.Repository
	project *domain.CaptionProject
	started chan struct{}
	once    sync.Once
}

func (r *cancelSnapshotRepo) Get(context.Context, string) (*domain.CaptionProject, error) {
	return r.project, nil
}

func (r *cancelSnapshotRepo) RevisionCues(ctx context.Context, _ string, _ int64) ([]domain.CaptionCue, string, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return nil, "", ctx.Err()
}

func TestReverificationEvidenceCancellationPropagation(t *testing.T) {
	repo := &cancelSnapshotRepo{project: &domain.CaptionProject{
		ID:       "project-1",
		Revision: 2,
		Status:   domain.StatusChanges,
		Cues: []domain.CaptionCue{{
			ID: "cue-1", ProjectID: "project-1", Sequence: 1,
			StartMS: 0, EndMS: 1000, CueRevision: 2, Text: "修订后文本",
		}},
		Findings: []domain.ReviewFinding{{
			ID: "finding-1", CueID: "cue-1", Status: domain.FindingRemediated,
			ReportedCueRevision: 1, ResolvedCueRevision: 2, EvidenceValid: true,
		}},
	}}
	repo.started = make(chan struct{})
	service := application.New(repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := service.ReverificationEvidenceSummary(ctx, "project-1", 2)
		errCh <- err
	}()
	<-repo.started
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("历史快照读取取消应沿调用链返回 context.Canceled，实际 err=%v", err)
	}
}
