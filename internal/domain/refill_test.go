package domain

import (
	"testing"
	"time"
)

func TestCompareCueSnapshotsFieldLevel(t *testing.T) {
	old := []CaptionCue{{ID: "a", StartMS: 0, EndMS: 1000, Speaker: "甲", Text: "原文"}, {ID: "b", StartMS: 1100, EndMS: 2000, Text: "保留"}}
	current := append([]CaptionCue(nil), old...)
	current[1].Text = "新文"
	diff := CompareCueSnapshots("p", 2, 3, old, current, "old", "new")
	if len(diff.Changes) != 1 || diff.Changes[0].CueID != "b" || len(diff.Changes[0].Changes) != 1 || diff.Changes[0].Changes[0].Field != "text" {
		t.Fatalf("字段差异不准确: %#v", diff)
	}
}

func TestBatchCommandsAreAtomic(t *testing.T) {
	p := testProject(t)
	now := time.Now()
	if err := p.SaveCues([]CaptionCue{{ID: "a", StartMS: 0, EndMS: 1000, Speaker: "甲", Text: "旧"}, {ID: "b", StartMS: 1100, EndMS: 2100, Speaker: "乙", Text: "旧"}}, now); err != nil {
		t.Fatal(err)
	}
	p.Status = StatusChanges
	p.Findings = []ReviewFinding{{ID: "f1", ProjectID: p.ID, CueID: "a", Status: FindingOpen, ReportedCueRevision: 1}, {ID: "f2", ProjectID: p.ID, CueID: "b", Status: FindingOpen, ReportedCueRevision: 1}}
	p.Cues[0].CueRevision, p.Cues[1].CueRevision = 2, 2
	if err := p.RemediateBatch([]RemediationItem{{FindingID: "f1", ResolutionNote: "完成", CueRevision: 2}, {FindingID: "f2", CueRevision: 2}}, now); err == nil {
		t.Fatal("空说明应拒绝")
	}
	if p.Findings[0].Status != FindingOpen {
		t.Fatal("失败批次产生了部分更新")
	}
	if err := p.RemediateBatch([]RemediationItem{{FindingID: "f1", ResolutionNote: "完成", CueRevision: 2}, {FindingID: "f2", ResolutionNote: "完成", CueRevision: 2}}, now); err != nil {
		t.Fatal(err)
	}
	p.Status = StatusReverification
	yes, no := true, false
	if err := p.VerifyBatch([]VerificationItem{{FindingID: "f1", Resolved: &yes, CueRevision: 2}, {FindingID: "f2", Resolved: &no, CueRevision: 2}}, "审校员", now); err != nil {
		t.Fatal(err)
	}
	if p.Findings[0].Status != FindingResolved || p.Findings[1].Status != FindingRejected || p.Status != StatusChanges {
		t.Fatalf("批量结论不准确: %#v", p.Findings)
	}
}
