package auditdetailcachealias_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

func TestAuditDetailCacheIsolation(t *testing.T) {
	repo := &revisionRepository{
		project: &domain.CaptionProject{ID: "project-a", Revision: 2, Status: domain.StatusDraft},
		event: domain.AuditEvent{
			ID: 7, ProjectID: "project-a", Type: "cues.saved", Actor: "制作员", Revision: 2,
			Detail: map[string]any{"source": "timeline"},
		},
		from: []domain.CaptionCue{{ID: "cue-a", Text: "第一版"}},
		to:   []domain.CaptionCue{{ID: "cue-a", Text: "第二版"}},
	}
	service := application.NewWithDependencies(repo, time.Now, func(prefix string) string { return prefix + "-id" })

	first, err := service.AuditEventDetail(context.Background(), "project-a", 7, "revision_diff", 2)
	if err != nil {
		t.Fatalf("首次查询审计详情失败: %v", err)
	}
	if first.RevisionDiff == nil || len(first.RevisionDiff.Changes) == 0 || len(first.RevisionDiff.Changes[0].Changes) == 0 {
		t.Fatalf("复现数据没有生成字段差异: %#v", first.RevisionDiff)
	}
	first.Event.Detail["source"] = "caller-polluted"
	first.RevisionDiff.Changes[0].Changes[0].NewValue = "caller-polluted"

	second, err := service.AuditEventDetail(context.Background(), "project-a", 7, "revision_diff", 2)
	if err != nil {
		t.Fatalf("第二次查询审计详情失败: %v", err)
	}
	if second.Event.Detail["source"] != "timeline" || second.RevisionDiff.Changes[0].Changes[0].NewValue != "第二版" {
		t.Fatalf("缓存返回了被前一调用方污染的审计详情: source=%v new_value=%q", second.Event.Detail["source"], second.RevisionDiff.Changes[0].Changes[0].NewValue)
	}
}

type revisionRepository struct {
	project *domain.CaptionProject
	event   domain.AuditEvent
	from    []domain.CaptionCue
	to      []domain.CaptionCue
}

func (r *revisionRepository) Get(context.Context, string) (*domain.CaptionProject, error) {
	copyProject := *r.project
	return &copyProject, nil
}

func (r *revisionRepository) AuditEvent(context.Context, string, int64) (*domain.AuditEvent, error) {
	copyEvent := r.event
	copyEvent.Detail = map[string]any{"source": r.event.Detail["source"]}
	return &copyEvent, nil
}

func (r *revisionRepository) RevisionCues(_ context.Context, _ string, revision int64) ([]domain.CaptionCue, string, error) {
	if revision == 1 {
		return append([]domain.CaptionCue(nil), r.from...), "from-checksum", nil
	}
	if revision == 2 {
		return append([]domain.CaptionCue(nil), r.to...), "to-checksum", nil
	}
	return nil, "", errors.New("unexpected revision")
}

func (*revisionRepository) Create(context.Context, *domain.CaptionProject, string, string) (*domain.MutationResult, bool, error) {
	return nil, false, errors.New("unexpected Create")
}
func (*revisionRepository) FindByMediaChecksum(context.Context, string) (*domain.ProjectSummary, error) {
	return nil, errors.New("unexpected FindByMediaChecksum")
}
func (*revisionRepository) Mutate(context.Context, domain.Mutation, func(*domain.CaptionProject) (any, error)) (*domain.MutationResult, bool, error) {
	return nil, false, errors.New("unexpected Mutate")
}
func (*revisionRepository) List(context.Context) ([]domain.ProjectSummary, error) {
	return nil, errors.New("unexpected List")
}
func (*revisionRepository) ListFiltered(context.Context, domain.QueueFilter) ([]domain.ProjectSummary, domain.QueueStats, error) {
	return nil, domain.QueueStats{}, errors.New("unexpected ListFiltered")
}
func (*revisionRepository) Audit(context.Context, string, int64, int) ([]domain.AuditEvent, error) {
	return nil, errors.New("unexpected Audit")
}
func (*revisionRepository) AuditQuery(context.Context, string, domain.AuditQuery) (domain.AuditPage, error) {
	return domain.AuditPage{}, errors.New("unexpected AuditQuery")
}
func (*revisionRepository) CueAtRevision(context.Context, string, string, int64) (*domain.CaptionCue, error) {
	return nil, errors.New("unexpected CueAtRevision")
}
func (*revisionRepository) Manifest(context.Context, string) (*domain.ReleaseManifest, error) {
	return nil, errors.New("unexpected Manifest")
}
func (*revisionRepository) Ready(context.Context) error { return errors.New("unexpected Ready") }
func (*revisionRepository) Close() error                { return nil }
