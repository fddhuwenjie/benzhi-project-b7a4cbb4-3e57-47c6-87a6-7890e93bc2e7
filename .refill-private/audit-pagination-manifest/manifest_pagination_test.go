package auditpaginationmanifest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
	"caption-release-workbench/internal/store"
)

func TestManifestReportFollowsAuditPagination(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "pagination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	seq := 0
	service := application.NewWithDependencies(repo, func() time.Time { return now }, func(prefix string) string {
		seq++
		return fmt.Sprintf("%s-%d", prefix, seq)
	})
	ctx := context.Background()
	created, _, err := service.CreateProject(ctx, application.CreateProjectCommand{
		RequestID: "create", ID: "pagination-project", Title: "分页审计节目", DurationMS: 10000,
		Language: "zh-CN", MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StyleProfile: "规范", Assignee: "制作员", Actor: "制作员",
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := created.Project.Revision
	cue := domain.CaptionCue{ID: "cue-1", StartMS: 0, EndMS: 2000, Speaker: "主播", Text: "测试字幕"}
	// 每次保存都会追加一条审计事件；超过常见的 200 条首屏上限后，批准事件位于后续页。
	for i := 0; i < 205; i++ {
		result, _, saveErr := service.SaveCues(ctx, "pagination-project", application.SaveCuesCommand{
			WriteMeta: application.WriteMeta{RequestID: fmt.Sprintf("save-%d", i), ExpectedRevision: revision, Actor: "制作员"},
			Cues:      []domain.CaptionCue{cue},
		})
		if saveErr != nil {
			t.Fatalf("第 %d 次保存失败: %v", i, saveErr)
		}
		revision = result.Project.Revision
	}
	checked, _, err := service.RunChecks(ctx, "pagination-project", application.CheckCommand{WriteMeta: application.WriteMeta{RequestID: "checks", ExpectedRevision: revision, Actor: "制作员"}})
	if err != nil {
		t.Fatal(err)
	}
	submitted, _, err := service.SubmitReview(ctx, "pagination-project", application.SubmitReviewCommand{WriteMeta: application.WriteMeta{RequestID: "submit", ExpectedRevision: checked.Project.Revision, Actor: "制作员"}})
	if err != nil {
		t.Fatal(err)
	}
	ready, _, err := service.ReviewDecision(ctx, "pagination-project", application.ReviewDecisionCommand{WriteMeta: application.WriteMeta{RequestID: "review", ExpectedRevision: submitted.Project.Revision, Actor: "审校员"}, Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	checked, _, err = service.RunChecks(ctx, "pagination-project", application.CheckCommand{WriteMeta: application.WriteMeta{RequestID: "release-checks", ExpectedRevision: ready.Project.Revision, Actor: "制作员"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.ReleasePreview(ctx, "pagination-project")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.Approve(ctx, "pagination-project", application.ApproveCommand{
		WriteMeta:  application.WriteMeta{RequestID: "approve", ExpectedRevision: checked.Project.Revision, Actor: "负责人"},
		ApprovedBy: "负责人", PreviewRevision: preview.CurrentRevision, CaptionChecksum: preview.CaptionChecksum, ConfirmationToken: preview.ConfirmationToken,
	}); err != nil {
		t.Fatal(err)
	}

	report, err := service.ManifestReport(ctx, "pagination-project")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Integrity.Complete {
		t.Fatalf("发布清单报告未找到跨页批准审计事件: %#v", report.Integrity)
	}
}
