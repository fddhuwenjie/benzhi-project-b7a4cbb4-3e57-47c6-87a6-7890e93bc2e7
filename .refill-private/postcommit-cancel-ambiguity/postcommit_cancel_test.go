package postcommitcancelambiguity

import (
	"context"
	"errors"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

func TestPostCommitCancellationDoesNotHideMutation(t *testing.T) {
	project, err := domain.CreateProject(domain.NewProject{
		ID:            "project-a",
		Title:         "晚间新闻",
		DurationMS:    10_000,
		Language:      "zh-CN",
		MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StyleProfile:  "广播字幕规范",
		Assignee:      "制作员甲",
	}, time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	repo := &commitThenCancelRepository{project: project, afterCommit: cancel}
	service := application.NewWithDependencies(repo, func() time.Time {
		return time.Date(2026, 8, 26, 10, 1, 0, 0, time.UTC)
	}, func(prefix string) string { return prefix + "-fixed" })

	result, replay, callErr := service.SaveCues(ctx, project.ID, application.SaveCuesCommand{
		WriteMeta: application.WriteMeta{RequestID: "save-after-cancel", ExpectedRevision: 1, Actor: "制作员甲"},
		Cues:      []domain.CaptionCue{{ID: "cue-1", StartMS: 0, EndMS: 2_000, Speaker: "主播", Text: "本台消息。"}},
	})
	committed := repo.committedProject()
	if committed.Revision != 2 || len(committed.Cues) != 1 {
		t.Fatalf("受控仓储未提交预期状态: revision=%d cues=%d", committed.Revision, len(committed.Cues))
	}
	if callErr != nil || result == nil || result.Project.Revision != 2 || replay {
		t.Fatalf("TestPostCommitCancellationDoesNotHideMutation: 写事务已提交 revision=2，却返回 result=%#v replay=%v err=%v", result, replay, callErr)
	}
}

type commitThenCancelRepository struct {
	project     *domain.CaptionProject
	afterCommit context.CancelFunc
}

func (r *commitThenCancelRepository) Mutate(ctx context.Context, mutation domain.Mutation, change func(*domain.CaptionProject) (any, error)) (*domain.MutationResult, bool, error) {
	working := cloneProject(r.project)
	value, err := change(working)
	if err != nil {
		return nil, false, err
	}
	working.Revision++
	r.project = cloneProject(working)
	r.afterCommit()
	return &domain.MutationResult{Project: cloneProject(working), Value: value}, false, nil
}

func (r *commitThenCancelRepository) Get(ctx context.Context, id string) (*domain.CaptionProject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id != r.project.ID {
		return nil, domain.NotFound("项目", id)
	}
	return cloneProject(r.project), nil
}

func (r *commitThenCancelRepository) committedProject() *domain.CaptionProject {
	return cloneProject(r.project)
}

func cloneProject(project *domain.CaptionProject) *domain.CaptionProject {
	copyProject := *project
	copyProject.Cues = append([]domain.CaptionCue(nil), project.Cues...)
	return &copyProject
}

func (r *commitThenCancelRepository) Create(context.Context, *domain.CaptionProject, string, string) (*domain.MutationResult, bool, error) {
	return nil, false, errors.New("unexpected Create")
}
func (r *commitThenCancelRepository) FindByMediaChecksum(context.Context, string) (*domain.ProjectSummary, error) {
	return nil, errors.New("unexpected FindByMediaChecksum")
}
func (r *commitThenCancelRepository) List(context.Context) ([]domain.ProjectSummary, error) {
	return nil, errors.New("unexpected List")
}
func (r *commitThenCancelRepository) ListFiltered(context.Context, domain.QueueFilter) ([]domain.ProjectSummary, domain.QueueStats, error) {
	return nil, domain.QueueStats{}, errors.New("unexpected ListFiltered")
}
func (r *commitThenCancelRepository) Audit(context.Context, string, int64, int) ([]domain.AuditEvent, error) {
	return nil, errors.New("unexpected Audit")
}
func (r *commitThenCancelRepository) AuditQuery(context.Context, string, domain.AuditQuery) (domain.AuditPage, error) {
	return domain.AuditPage{}, errors.New("unexpected AuditQuery")
}
func (r *commitThenCancelRepository) AuditEvent(context.Context, string, int64) (*domain.AuditEvent, error) {
	return nil, errors.New("unexpected AuditEvent")
}
func (r *commitThenCancelRepository) RevisionCues(context.Context, string, int64) ([]domain.CaptionCue, string, error) {
	return nil, "", errors.New("unexpected RevisionCues")
}
func (r *commitThenCancelRepository) CueAtRevision(context.Context, string, string, int64) (*domain.CaptionCue, error) {
	return nil, errors.New("unexpected CueAtRevision")
}
func (r *commitThenCancelRepository) Manifest(context.Context, string) (*domain.ReleaseManifest, error) {
	return nil, errors.New("unexpected Manifest")
}
func (r *commitThenCancelRepository) Ready(context.Context) error { return nil }
func (r *commitThenCancelRepository) Close() error                { return nil }
