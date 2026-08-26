package domain

import (
	"errors"
	"testing"
	"time"
)

func TestRiskAndCueSearch(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	p := testProject(t)
	p.UpdatedAt = now.Add(-8 * 24 * time.Hour)
	p.Status = StatusChanges
	p.Checks = []RuleCheck{{ID: "check-1", CueID: "a", Rule: "reading_speed", Level: "error", Message: "过快", Passed: false}}
	p.CheckRuns = []RuleCheckRun{{ID: "run-1", ProjectRevision: p.Revision, Results: append([]RuleCheck(nil), p.Checks...)}}
	p.Findings = []ReviewFinding{{ID: "f1", ProjectID: p.ID, CueID: "a", Status: FindingOpen, Severity: "critical"}}
	risk := CalculateProjectRisk(p, now)
	if risk.Level != RiskHigh || risk.FailedRuleCount != 1 || risk.SevereFindingCount != 1 || risk.StaleDays != 8 {
		t.Fatalf("风险分层不准确: %#v", risk)
	}
	if second := CalculateProjectRisk(p, now); second.Score != risk.Score || second.Level != risk.Level {
		t.Fatalf("相同聚合风险结果不稳定: %#v / %#v", risk, second)
	}

	p.Status, p.Revision, p.DurationMS = StatusDraft, 4, 10000
	p.Cues = []CaptionCue{
		{ID: "a", ProjectID: p.ID, Sequence: 1, StartMS: 0, EndMS: 2000, Speaker: "HOST", Text: "前文", CueRevision: 1},
		{ID: "b", ProjectID: p.ID, Sequence: 2, StartMS: 2000, EndMS: 4000, Speaker: " Alice ", Text: "Hello   World", SoundDescription: "[MUSIC]", CueRevision: 2},
		{ID: "c", ProjectID: p.ID, Sequence: 3, StartMS: 4100, EndMS: 6000, Speaker: "Bob", Text: "后文", CueRevision: 1},
	}
	start, end := int64(2000), int64(4000)
	result, err := SearchCues(p, CueSearchQuery{Keyword: " hello world ", StartMS: &start, EndMS: &end, ExpectedRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Cue.ID != "b" || result.Hits[0].Previous.ID != "a" || result.Hits[0].Next.ID != "c" || result.RevisionMatches {
		t.Fatalf("检索命中或上下文不正确: %#v", result)
	}
	if _, err := SearchCues(p, CueSearchQuery{Keyword: "  "}); err == nil {
		t.Fatal("空关键词应返回字段级参数错误")
	}
}

func TestConvertFailuresAndEvidenceDiff(t *testing.T) {
	p := testProject(t)
	p.Status = StatusInReview
	p.Revision = 5
	p.Cues = []CaptionCue{{ID: "a", ProjectID: p.ID, Sequence: 1, StartMS: 0, EndMS: 1000, Speaker: "甲", Text: "旧文", CueRevision: 1}}
	failure := RuleCheck{ID: "result-1", CueID: "a", Rule: "reading_speed", Level: "error", Message: "阅读速度过快", Passed: false}
	p.Checks = []RuleCheck{failure}
	p.CheckRuns = []RuleCheckRun{{ID: "run-1", ProjectRevision: 4, Results: []RuleCheck{failure}}}
	ids := []string{"f1", "f2"}
	next := func() string { id := ids[0]; ids = ids[1:]; return id }
	created, err := p.ConvertCheckFailures("run-1", "审校员", []CheckFindingSelection{{CheckID: "result-1", CueID: "a"}}, next, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].Category != "accessibility" || created[0].Severity != "major" || created[0].SourceCheckRunID != "run-1" || created[0].SourceCheckRevision != 5 {
		t.Fatalf("转换结果来源不完整: %#v", created)
	}
	before := len(p.Findings)
	_, err = p.ConvertCheckFailures("run-1", "审校员", []CheckFindingSelection{{CheckID: "result-1", CueID: "a"}}, next, time.Now())
	var business *BusinessError
	if !errors.As(err, &business) || business.Code != CodeConflict || len(p.Findings) != before {
		t.Fatalf("重复转换应整批冲突且不写入: %v %#v", err, p.Findings)
	}

	finding := created[0]
	finding.ReportedCueRevision, finding.ResolvedCueRevision, finding.ResolutionNote, finding.EvidenceValid = 1, 2, "已修正正文", true
	reported := CaptionCue{ID: "a", Text: "旧文", Speaker: "甲", StartMS: 0, EndMS: 1000, CueRevision: 1}
	resolved := reported
	resolved.Text, resolved.CueRevision = "新文", 2
	current := resolved
	evidence := CompareFindingEvidence(p.ID, finding, &reported, &resolved, &current)
	if !evidence.Valid || len(evidence.Changes) != 1 || evidence.Changes[0].Field != "text" {
		t.Fatalf("字段级证据差异不正确: %#v", evidence)
	}
	current.CueRevision = 3
	if stale := CompareFindingEvidence(p.ID, finding, &reported, &resolved, &current); stale.Valid || stale.Status != "stale" {
		t.Fatalf("后续编辑应使证据过期: %#v", stale)
	}
}

func TestCueSplitPreviewAndApply(t *testing.T) {
	p := testProject(t)
	p.Revision = 4
	p.Cues = []CaptionCue{
		{ID: "a", ProjectID: p.ID, Sequence: 1, StartMS: 0, EndMS: 4000, Speaker: "主播", Text: "第一句。第二句。", SoundDescription: "[音乐]", CueRevision: 2},
		{ID: "b", ProjectID: p.ID, Sequence: 2, StartMS: 4500, EndMS: 6000, Speaker: "记者", Text: "后文", CueRevision: 1},
	}
	p.Checks = []RuleCheck{{ID: "check", CueID: "a", Rule: "reading_speed", Level: "error", Passed: true}}
	p.Findings = []ReviewFinding{{ID: "f", ProjectID: p.ID, CueID: "a", Category: "accuracy", Severity: "major", Description: "需复核", Status: FindingResolved, ReportedBy: "审校员", ReportedCueRevision: 1, ResolvedCueRevision: 2, EvidenceValid: true}}
	preview, err := p.PreviewCueSplit("a", 2000, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if preview.First.Text != "第一句。" || preview.Second.Text != "第二句。" || preview.Second.SoundDescription != "" {
		t.Fatalf("拆分预览分配错误: %#v", preview)
	}
	result, err := p.ApplyCueSplit("a", "new", 2000, 4, 4, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Cues) != 3 || p.Cues[0].ID != "a" || p.Cues[1].ID != "new" || p.Cues[2].Sequence != 3 || result.NewCueID != "new" {
		t.Fatalf("拆分确认结果错误: %#v", p.Cues)
	}
	if len(p.Checks) != 0 || p.Findings[0].EvidenceValid {
		t.Fatalf("拆分后规则或证据未失效: %#v %#v", p.Checks, p.Findings)
	}
	if _, err := p.PreviewCueSplit("a", p.Cues[0].StartMS, 1, 4); err == nil {
		t.Fatal("起点拆分应失败")
	}
}

func TestFindingWorklistFiltersStatsAndRevision(t *testing.T) {
	p := testProject(t)
	p.Status, p.Revision = StatusInReview, 8
	p.Cues = []CaptionCue{
		{ID: "a", ProjectID: p.ID, Sequence: 1, StartMS: 0, EndMS: 1000, Text: "第一段", CueRevision: 1},
		{ID: "b", ProjectID: p.ID, Sequence: 2, StartMS: 1200, EndMS: 2200, Text: "第二段", CueRevision: 2},
	}
	p.Findings = []ReviewFinding{
		{ID: "critical", ProjectID: p.ID, CueID: "b", Category: "timing", Severity: "critical", Description: "时间轴重叠", Status: FindingOpen, ReportedCueRevision: 2},
		{ID: "minor", ProjectID: p.ID, CueID: "a", Category: "style", Severity: "minor", Description: "样式", Status: FindingOpen, ReportedCueRevision: 1},
		{ID: "done", ProjectID: p.ID, CueID: "a", Category: "accuracy", Severity: "major", Description: "内容", Status: FindingResolved, ReportedCueRevision: 1, ResolvedCueRevision: 1, EvidenceValid: true},
	}
	worklist, err := QueryFindingWorklist(p, FindingWorklistQuery{Statuses: []FindingStatus{FindingStatus("unclosed")}, Severities: []string{"critical"}, ExpectedRevision: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(worklist.Items) != 1 || worklist.Items[0].Finding.ID != "critical" || worklist.Stats.DistinctCueCount != 1 || worklist.Stats.WithoutEvidenceCount != 1 || worklist.RevisionMatches || worklist.BulkActionsAllowed {
		t.Fatalf("问题工作清单错误: %#v", worklist)
	}
}

func TestFrozenVerificationPackage(t *testing.T) {
	p := testProject(t)
	p.Status, p.Revision = StatusReleased, 6
	p.Cues = []CaptionCue{{ID: "a", ProjectID: p.ID, Sequence: 1, StartMS: 0, EndMS: 2000, Speaker: "主播", Text: "正文", CueRevision: 1}}
	approvedAt := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	p.Manifest = &ReleaseManifest{ID: "m1", ProjectID: p.ID, ProjectRevision: 6, CueCount: 1, CaptionChecksum: p.CaptionChecksum(), MediaChecksum: p.MediaChecksum, ApprovedBy: "负责人", ApprovedAt: approvedAt, ManifestVersion: "1"}
	audit := &AuditEvent{ID: 9, ProjectID: p.ID, Type: "release.approved", Actor: "负责人", Revision: 6, CreatedAt: approvedAt}
	pack, integrity := BuildVerificationPackage(p, p.Manifest, append([]CaptionCue(nil), p.Cues...), p.CaptionChecksum(), audit)
	if !integrity.Complete || pack.ProjectRevision != 6 || len(pack.Captions) != 1 || pack.Approval.AuditEventID != 9 {
		t.Fatalf("冻结核验包错误: %#v %#v", pack, integrity)
	}
	tampered := append([]CaptionCue(nil), p.Cues...)
	tampered[0].Text = "被篡改"
	_, integrity = BuildVerificationPackage(p, p.Manifest, tampered, p.CaptionChecksum(), audit)
	if integrity.Complete {
		t.Fatal("篡改冻结字幕后完整性不应通过")
	}
}
