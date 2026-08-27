package finding_evidence_error_cache_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

var errSnapshotTemporarilyUnavailable = errors.New("snapshot storage temporarily unavailable")

type recoveringRepository struct {
	domain.Repository
	mu        sync.Mutex
	project   *domain.CaptionProject
	unhealthy bool
	reads     int
}

func (r *recoveringRepository) Get(context.Context, string) (*domain.CaptionProject, error) {
	copyProject := *r.project
	copyProject.Cues = append([]domain.CaptionCue(nil), r.project.Cues...)
	copyProject.Findings = append([]domain.ReviewFinding(nil), r.project.Findings...)
	return &copyProject, nil
}

func (r *recoveringRepository) CueAtRevision(_ context.Context, projectID, cueID string, revision int64) (*domain.CaptionCue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if r.unhealthy {
		return nil, fmt.Errorf("read %s/%s@%d: %w", projectID, cueID, revision, errSnapshotTemporarilyUnavailable)
	}
	return &domain.CaptionCue{
		ID: cueID, ProjectID: projectID, Sequence: 1,
		StartMS: 0, EndMS: 1000, Speaker: "播音员",
		Text: fmt.Sprintf("revision-%d", revision), CueRevision: revision,
	}, nil
}

func (r *recoveringRepository) recoverStorage() {
	r.mu.Lock()
	r.unhealthy = false
	r.mu.Unlock()
}

func (r *recoveringRepository) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func TestFindingEvidenceTransientErrorRecovery(t *testing.T) {
	project := &domain.CaptionProject{
		ID: "project-recovery", Revision: 3, Status: domain.StatusChanges,
		Cues: []domain.CaptionCue{{
			ID: "cue-1", ProjectID: "project-recovery", Sequence: 1,
			StartMS: 0, EndMS: 1000, Speaker: "播音员", Text: "revision-2", CueRevision: 2,
		}},
		Findings: []domain.ReviewFinding{{
			ID: "finding-1", ProjectID: "project-recovery", CueID: "cue-1",
			Status: domain.FindingRemediated, ReportedCueRevision: 1,
			ResolvedCueRevision: 2, ResolutionNote: "已修订", EvidenceValid: true,
		}},
	}
	repository := &recoveringRepository{project: project, unhealthy: true}
	service := application.New(repository)

	_, err := service.FindingEvidence(context.Background(), project.ID, "finding-1", project.Revision)
	if !errors.Is(err, errSnapshotTemporarilyUnavailable) {
		t.Fatalf("首次读取应保留瞬时存储错误链，实际为 %v", err)
	}
	if reads := repository.readCount(); reads != 1 {
		t.Fatalf("首次失败应只读取一份快照，实际读取 %d 次", reads)
	}

	repository.recoverStorage()
	evidence, err := service.FindingEvidence(context.Background(), project.ID, "finding-1", project.Revision)
	if err != nil {
		t.Fatalf("存储恢复后仍返回上次请求的瞬时错误: %v", err)
	}
	if reads := repository.readCount(); reads != 3 {
		t.Fatalf("恢复后的请求应重新读取两份历史快照，累计读取应为 3 次，实际为 %d", reads)
	}
	if evidence == nil || !evidence.Valid || evidence.Status != "valid" {
		t.Fatalf("存储恢复后应返回有效证据，实际为 %#v", evidence)
	}
}
