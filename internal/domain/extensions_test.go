package domain

import (
	"errors"
	"testing"
	"time"
)

func TestMediaChecksumAndCueShift(t *testing.T) {
	if _, err := CreateProject(NewProject{ID: "bad", Title: "节目", DurationMS: 1000, Language: "zh", MediaChecksum: " abcdef ", StyleProfile: "规范", Assignee: "甲"}, time.Now()); err == nil {
		t.Fatal("应拒绝带空白或非 64 位的素材校验值")
	}
	p := testProject(t)
	if err := p.SaveCues([]CaptionCue{{ID: "a", StartMS: 0, EndMS: 1000, Text: "A"}, {ID: "b", StartMS: 1500, EndMS: 2500, Text: "B"}, {ID: "c", StartMS: 3000, EndMS: 4000, Text: "C"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	preview, err := p.PreviewCueShift([]string{"b"}, 250)
	if err != nil || preview.Changes[0].NewStartMS != 1750 {
		t.Fatalf("预览无效: %#v %v", preview, err)
	}
	if _, err := p.ApplyCueShift([]string{"b"}, 250, p.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	if p.Cues[1].CueRevision != 2 || p.Cues[0].CueRevision != 1 {
		t.Fatalf("字幕版本递增错误: %#v", p.Cues)
	}
	before := p.Cues[2]
	if _, err := p.ApplyCueShift([]string{"c"}, p.DurationMS, p.Revision, time.Now()); err == nil {
		t.Fatal("越界偏移应整批拒绝")
	}
	if p.Cues[2] != before {
		t.Fatal("失败偏移不应产生部分更新")
	}
}

func TestFindingBatchDuplicatesAndEvidence(t *testing.T) {
	p := testProject(t)
	if err := p.SaveCues([]CaptionCue{{ID: "a", StartMS: 0, EndMS: 2000, Speaker: "甲", Text: "原文"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	p.RunChecksForRevision("run-1", p.Revision, time.Now())
	if err := p.SubmitReview(time.Now()); err != nil {
		t.Fatal(err)
	}
	batch := []ReviewFinding{{ID: "f1", CueID: "a", Category: "accuracy", Severity: "major", Description: " 名称   错误 ", ReportedBy: "乙"}, {ID: "f2", CueID: "a", Category: "accuracy", Severity: "minor", Description: "名称 错误", ReportedBy: "乙"}}
	if err := p.AddFindings(batch, time.Now()); err == nil {
		t.Fatal("批次内规范化重复应被拦截")
	}
	if len(p.Findings) != 0 {
		t.Fatal("失败批次不应部分写入")
	}
	if err := p.AddFinding(batch[0], time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := p.ReviewDecision(false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := p.Remediate("f1", "只写说明", time.Now()); err == nil {
		t.Fatal("未修改字幕不能登记整改")
	}
	if err := p.SaveCues([]CaptionCue{{ID: "a", StartMS: 0, EndMS: 2000, Speaker: "甲", Text: "新文"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := p.Remediate("f1", "已修正", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveCues([]CaptionCue{{ID: "a", StartMS: 0, EndMS: 2000, Speaker: "甲", Text: "再次修改"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if p.Findings[0].EvidenceValid {
		t.Fatal("后续字幕修改应使证据过期")
	}
}

func TestCheckRunsAndReleasePreview(t *testing.T) {
	p := testProject(t)
	if err := p.SaveCues([]CaptionCue{{ID: "a", StartMS: 0, EndMS: 2000, Speaker: "甲", Text: "内容"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	p.RunChecksForRevision("run-1", p.Revision, time.Now())
	p.Cues[0].Speaker = ""
	p.RunChecksForRevision("run-2", p.Revision, time.Now())
	diff := CheckRunDiff(p.CheckRuns)
	if len(diff.NewFailures) != 1 {
		t.Fatalf("应识别新增失败: %#v", diff)
	}
	p.Status = StatusReady
	preview := p.ReleasePreview()
	if len(preview.Blockers) == 0 {
		t.Fatal("检查失败应阻断发布")
	}
	if _, err := p.ApprovePreview("负责人", "m1", preview.CurrentRevision, "wrong", preview.ConfirmationToken, time.Now()); err == nil {
		t.Fatal("错误摘要应拒绝批准")
	} else {
		var business *BusinessError
		if !errors.As(err, &business) {
			t.Fatal(err)
		}
	}
}
