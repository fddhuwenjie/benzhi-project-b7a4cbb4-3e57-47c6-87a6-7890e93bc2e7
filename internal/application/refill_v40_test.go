package application

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"caption-release-workbench/internal/domain"
	"caption-release-workbench/internal/store"
)

func TestSplitIdempotencyAndFrozenPackageFlow(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "refill-v40.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)
	sequence := 0
	service := NewWithDependencies(repo, func() time.Time { return now }, func(prefix string) string {
		sequence++
		return fmt.Sprintf("%s-%d", prefix, sequence)
	})
	ctx := context.Background()
	created, _, err := service.CreateProject(ctx, CreateProjectCommand{RequestID: "create", ID: "project", Title: "节目", DurationMS: 10000, Language: "zh-CN", MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", StyleProfile: "规范", Assignee: "制作员", Actor: "制作员"})
	if err != nil {
		t.Fatal(err)
	}
	saved, _, err := service.SaveCues(ctx, "project", SaveCuesCommand{WriteMeta: WriteMeta{RequestID: "save", ExpectedRevision: created.Project.Revision, Actor: "制作员"}, Cues: []domain.CaptionCue{{ID: "a", StartMS: 0, EndMS: 4000, Speaker: "主播", Text: "第一句。第二句。"}, {ID: "b", StartMS: 4500, EndMS: 6500, Speaker: "主播", Text: "后文"}}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewCueSplit(ctx, "project", "a", 2000, 4, saved.Project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	cmd := SplitCueCommand{WriteMeta: WriteMeta{RequestID: "split", ExpectedRevision: saved.Project.Revision, Actor: "制作员"}, SplitTimeMS: 2000, TextOffset: 4, PreviewRevision: preview.ProjectRevision}
	result, replay, err := service.SplitCue(ctx, "project", "a", cmd)
	if err != nil || replay || len(result.Project.Cues) != 3 {
		t.Fatalf("首次拆分失败: replay=%v err=%v result=%#v", replay, err, result)
	}
	replayed, replay, err := service.SplitCue(ctx, "project", "a", cmd)
	if err != nil || !replay || replayed.Project.Revision != result.Project.Revision {
		t.Fatalf("拆分幂等重放失败: replay=%v err=%v", replay, err)
	}

	checked, _, err := service.RunChecks(ctx, "project", CheckCommand{WriteMeta{RequestID: "checks", ExpectedRevision: result.Project.Revision, Actor: "制作员"}})
	if err != nil {
		t.Fatal(err)
	}
	submitted, _, err := service.SubmitReview(ctx, "project", SubmitReviewCommand{WriteMeta{RequestID: "submit", ExpectedRevision: checked.Project.Revision, Actor: "制作员"}})
	if err != nil {
		t.Fatal(err)
	}
	ready, _, err := service.ReviewDecision(ctx, "project", ReviewDecisionCommand{WriteMeta: WriteMeta{RequestID: "review", ExpectedRevision: submitted.Project.Revision, Actor: "审校员"}, Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	checked, _, err = service.RunChecks(ctx, "project", CheckCommand{WriteMeta{RequestID: "release-checks", ExpectedRevision: ready.Project.Revision, Actor: "制作员"}})
	if err != nil {
		t.Fatal(err)
	}
	releasePreview, err := service.ReleasePreview(ctx, "project")
	if err != nil || len(releasePreview.Blockers) != 0 {
		t.Fatalf("发布预检失败: %v %#v", err, releasePreview)
	}
	released, _, err := service.Approve(ctx, "project", ApproveCommand{WriteMeta: WriteMeta{RequestID: "approve", ExpectedRevision: checked.Project.Revision, Actor: "负责人"}, ApprovedBy: "负责人", PreviewRevision: releasePreview.CurrentRevision, CaptionChecksum: releasePreview.CaptionChecksum, ConfirmationToken: releasePreview.ConfirmationToken})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.VerificationPackageSummary(ctx, "project")
	if err != nil || !summary.DownloadReady || summary.ProjectRevision != released.Project.Revision {
		t.Fatalf("核验包摘要失败: err=%v summary=%#v", err, summary)
	}
	pack, err := service.VerificationPackage(ctx, "project")
	if err != nil || len(pack.Captions) != 3 || pack.CaptionChecksum != released.Project.Manifest.CaptionChecksum {
		t.Fatalf("冻结核验包失败: err=%v pack=%#v", err, pack)
	}
	after, err := service.GetProject(ctx, "project")
	if err != nil || after.Revision != released.Project.Revision {
		t.Fatalf("只读核验不应改变修订: err=%v project=%#v", err, after)
	}
}
