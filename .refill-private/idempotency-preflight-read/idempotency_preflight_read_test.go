package idempotency_preflight_read_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type replayRepository struct {
	project     *domain.CaptionProject
	cached      *domain.MutationResult
	getErr      error
	mutateCalls int
}

func (r *replayRepository) Get(context.Context, string) (*domain.CaptionProject, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	copyProject := *r.project
	copyProject.Cues = append([]domain.CaptionCue(nil), r.project.Cues...)
	return &copyProject, nil
}

func (r *replayRepository) Mutate(_ context.Context, mutation domain.Mutation, change func(*domain.CaptionProject) (any, error)) (*domain.MutationResult, bool, error) {
	r.mutateCalls++
	if r.cached != nil {
		return r.cached, true, nil
	}
	if mutation.ExpectedRevision != r.project.Revision {
		return nil, false, domain.Conflict("修订冲突")
	}
	value, err := change(r.project)
	if err != nil {
		return nil, false, err
	}
	r.project.Revision++
	r.cached = &domain.MutationResult{Project: r.project, Value: value}
	return r.cached, false, nil
}

func (*replayRepository) Create(context.Context, *domain.CaptionProject, string, string) (*domain.MutationResult, bool, error) {
	panic("unexpected Create")
}
func (*replayRepository) FindByMediaChecksum(context.Context, string) (*domain.ProjectSummary, error) {
	panic("unexpected FindByMediaChecksum")
}
func (*replayRepository) List(context.Context) ([]domain.ProjectSummary, error) {
	panic("unexpected List")
}
func (*replayRepository) ListFiltered(context.Context, domain.QueueFilter) ([]domain.ProjectSummary, domain.QueueStats, error) {
	panic("unexpected ListFiltered")
}
func (*replayRepository) Audit(context.Context, string, int64, int) ([]domain.AuditEvent, error) {
	panic("unexpected Audit")
}
func (*replayRepository) AuditQuery(context.Context, string, domain.AuditQuery) (domain.AuditPage, error) {
	panic("unexpected AuditQuery")
}
func (*replayRepository) AuditEvent(context.Context, string, int64) (*domain.AuditEvent, error) {
	panic("unexpected AuditEvent")
}
func (*replayRepository) RevisionCues(context.Context, string, int64) ([]domain.CaptionCue, string, error) {
	panic("unexpected RevisionCues")
}
func (*replayRepository) CueAtRevision(context.Context, string, string, int64) (*domain.CaptionCue, error) {
	panic("unexpected CueAtRevision")
}
func (*replayRepository) Manifest(context.Context, string) (*domain.ReleaseManifest, error) {
	panic("unexpected Manifest")
}
func (*replayRepository) Ready(context.Context) error { panic("unexpected Ready") }
func (*replayRepository) Close() error                { return nil }

func TestIdempotentReplaySurvivesAggregateReadFailure(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	project, err := domain.CreateProject(domain.NewProject{
		ID: "project", Title: "节目", DurationMS: 10000, Language: "zh-CN",
		MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StyleProfile:  "规范", Assignee: "制作员",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	repo := &replayRepository{project: project}
	service := application.NewWithDependencies(repo, func() time.Time { return now }, func(prefix string) string { return prefix + "-1" })
	cmd := application.SaveCuesCommand{
		WriteMeta: application.WriteMeta{RequestID: "save-request", ExpectedRevision: 1, Actor: "制作员"},
		Cues:      []domain.CaptionCue{{ID: "cue-1", StartMS: 0, EndMS: 2000, Speaker: "主播", Text: "字幕内容"}},
	}
	first, replay, err := service.SaveCues(context.Background(), "project", cmd)
	if err != nil || replay || first.Project.Revision != 2 {
		t.Fatalf("首次写入失败: replay=%v err=%v result=%#v", replay, err, first)
	}

	repo.getErr = errors.New("聚合读取资源暂时不可用")
	second, replay, err := service.SaveCues(context.Background(), "project", cmd)
	if err != nil {
		t.Fatalf("已完成 request_id 的重放不应依赖聚合读取: %v", err)
	}
	if !replay || second.Project.Revision != first.Project.Revision {
		t.Fatalf("应返回已持久化的原始结果: replay=%v result=%#v", replay, second)
	}
	if repo.mutateCalls != 2 {
		t.Fatalf("重放必须进入 Repository.Mutate 的幂等索引，调用次数=%d", repo.mutateCalls)
	}
}
