package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// CalculateProjectRisk derives a queue-only value from the current aggregate.
// It deliberately does not persist the result, so every queue read reflects the
// latest project state and audit-driven UpdatedAt value.
func CalculateProjectRisk(p *CaptionProject, now time.Time) ProjectRisk {
	risk := ProjectRisk{Level: RiskLow, Reasons: []RiskReason{}}
	if len(p.CheckRuns) > 0 && len(p.Checks) > 0 {
		for _, result := range p.Checks {
			if !result.Passed && result.Level == "error" {
				risk.FailedRuleCount++
			}
		}
	}
	for _, finding := range p.Findings {
		if finding.Status == FindingResolved {
			continue
		}
		risk.OpenFindingCount++
		if finding.Severity == "major" || finding.Severity == "critical" {
			risk.SevereFindingCount++
		}
	}
	if !p.UpdatedAt.IsZero() && now.After(p.UpdatedAt) {
		risk.StaleDays = int(now.Sub(p.UpdatedAt) / (24 * time.Hour))
	}
	if risk.FailedRuleCount > 0 {
		risk.Score += risk.FailedRuleCount * 4
		risk.Reasons = append(risk.Reasons, RiskReason{Code: "failed_rules", Message: "当前修订存在错误级规则失败", Count: risk.FailedRuleCount})
	}
	if risk.SevereFindingCount > 0 {
		risk.Score += risk.SevereFindingCount * 3
		risk.Reasons = append(risk.Reasons, RiskReason{Code: "severe_findings", Message: "存在未关闭的主要或严重审校问题", Count: risk.SevereFindingCount})
	}
	if p.Status == StatusChanges || p.Status == StatusReverification {
		risk.Score += 2
		risk.Reasons = append(risk.Reasons, RiskReason{Code: "remediation_state", Message: "项目正在整改或定向复验"})
	}
	if risk.StaleDays >= 7 {
		risk.Score += 2
		risk.Reasons = append(risk.Reasons, RiskReason{Code: "stale", Message: "项目已长期未更新", Count: risk.StaleDays})
	} else if risk.StaleDays >= 3 {
		risk.Score++
		risk.Reasons = append(risk.Reasons, RiskReason{Code: "aging", Message: "项目已有一段时间未更新", Count: risk.StaleDays})
	}
	switch {
	case risk.FailedRuleCount > 0 || risk.SevereFindingCount > 0 || risk.Score >= 5:
		risk.Level = RiskHigh
	case risk.Score >= 2 || risk.OpenFindingCount > 0:
		risk.Level = RiskMedium
	}
	if len(risk.Reasons) == 0 {
		risk.Reasons = append(risk.Reasons, RiskReason{Code: "clear", Message: "当前无规则失败或未关闭问题"})
	}
	return risk
}

func ValidRiskLevel(level RiskLevel) bool {
	return level == RiskLow || level == RiskMedium || level == RiskHigh
}

func SearchCues(p *CaptionProject, query CueSearchQuery) (*CueSearchResult, error) {
	keyword := compactFold(query.Keyword)
	if keyword == "" {
		return nil, Invalid("关键词不能为空", "keyword")
	}
	if query.StartMS != nil && *query.StartMS < 0 {
		return nil, Invalid("起始时间不能为负数", "start_ms")
	}
	if query.EndMS != nil && *query.EndMS < 0 {
		return nil, Invalid("结束时间不能为负数", "end_ms")
	}
	if query.StartMS != nil && query.EndMS != nil && *query.StartMS > *query.EndMS {
		return nil, Invalid("起始时间不能晚于结束时间", "start_ms", "end_ms")
	}
	if query.StartMS != nil && *query.StartMS > p.DurationMS {
		return nil, Invalid("起始时间超过节目时长", "start_ms")
	}
	if query.EndMS != nil && *query.EndMS > p.DurationMS {
		return nil, Invalid("结束时间超过节目时长", "end_ms")
	}
	ordered := append([]CaptionCue(nil), p.Cues...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartMS == ordered[j].StartMS {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		return ordered[i].StartMS < ordered[j].StartMS
	})
	result := &CueSearchResult{ProjectID: p.ID, ProjectRevision: p.Revision, RevisionMatches: query.ExpectedRevision == 0 || query.ExpectedRevision == p.Revision, ReadOnly: p.Status == StatusReleased, Hits: []CueSearchHit{}}
	for i := range ordered {
		cue := ordered[i]
		if query.StartMS != nil && cue.EndMS < *query.StartMS || query.EndMS != nil && cue.StartMS > *query.EndMS {
			continue
		}
		fields := []string{}
		for _, candidate := range []struct{ name, value string }{{"text", cue.Text}, {"speaker", cue.Speaker}, {"sound_description", cue.SoundDescription}} {
			if strings.Contains(compactFold(candidate.value), keyword) {
				fields = append(fields, candidate.name)
			}
		}
		if len(fields) == 0 {
			continue
		}
		hit := CueSearchHit{Cue: cue, MatchedFields: fields}
		if i > 0 {
			previous := ordered[i-1]
			hit.Previous = &previous
		}
		if i+1 < len(ordered) {
			next := ordered[i+1]
			hit.Next = &next
		}
		result.Hits = append(result.Hits, hit)
	}
	return result, nil
}

func compactFold(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(value))
}

func (p *CaptionProject) ConvertCheckFailures(runID, actor string, selections []CheckFindingSelection, nextID func() string, now time.Time) ([]ReviewFinding, error) {
	if p.Status != StatusInReview {
		return nil, Conflict("仅审校中可将规则失败转为审校问题")
	}
	runID, actor = strings.TrimSpace(runID), strings.TrimSpace(actor)
	if runID == "" || actor == "" || len(selections) == 0 {
		return nil, Invalid("检查运行、选择项和审校员不能为空", "check_run_id", "selections", "actor")
	}
	if len(p.CheckRuns) == 0 {
		return nil, Conflict("项目没有可转换的检查运行")
	}
	run := p.CheckRuns[len(p.CheckRuns)-1]
	if run.ID != runID || len(p.Checks) == 0 {
		return nil, ConflictWithDetails("检查结果已过期", map[string]any{"check_run_id": runID, "current_revision": p.Revision})
	}
	results := map[string]RuleCheck{}
	for _, result := range run.Results {
		results[result.ID+"\x00"+result.CueID] = result
	}
	prepared := make([]ReviewFinding, 0, len(selections))
	seen := map[string]bool{}
	for index, selection := range selections {
		key := strings.TrimSpace(selection.CheckID) + "\x00" + strings.TrimSpace(selection.CueID)
		if seen[key] {
			return nil, Conflict("转换批次内存在重复检查结果")
		}
		seen[key] = true
		check, ok := results[key]
		if !ok || check.Passed || check.CueID == "" {
			return nil, Invalid(fmt.Sprintf("第 %d 项不是可转换的未通过字幕规则", index+1), "selections")
		}
		category, severity := findingMapping(check)
		prepared = append(prepared, ReviewFinding{ID: nextID(), CueID: check.CueID, Category: category, Severity: severity, Description: check.Message, ReportedBy: actor, SourceCheckRunID: run.ID, SourceRule: check.Rule, SourceCheckRevision: p.Revision})
	}
	if err := p.AddFindings(prepared, now); err != nil {
		return nil, err
	}
	return prepared, nil
}

func findingMapping(check RuleCheck) (string, string) {
	category := "accessibility"
	if check.Rule == "caption_gap" {
		category = "timing"
	}
	severity := "minor"
	if check.Level == "error" {
		severity = "major"
	}
	return category, severity
}

func CompareFindingEvidence(projectID string, finding ReviewFinding, reported, resolved, current *CaptionCue) FindingEvidence {
	result := FindingEvidence{ProjectID: projectID, FindingID: finding.ID, CueID: finding.CueID, ReportedCueRevision: finding.ReportedCueRevision, ResolvedCueRevision: finding.ResolvedCueRevision, ResolutionNote: finding.ResolutionNote, Status: "invalid", Changes: []CueFieldChange{}}
	if current != nil {
		result.CurrentCueRevision = current.CueRevision
	}
	if reported == nil || resolved == nil {
		result.Status = "snapshot_missing"
		return result
	}
	if reported.ID != finding.CueID || resolved.ID != finding.CueID || finding.ResolvedCueRevision <= finding.ReportedCueRevision {
		result.Status = "revision_invalid"
		return result
	}
	diff := CompareCueSnapshots(projectID, finding.ReportedCueRevision, finding.ResolvedCueRevision, []CaptionCue{*reported}, []CaptionCue{*resolved}, "", "")
	if len(diff.Changes) > 0 {
		result.Changes = diff.Changes[0].Changes
	}
	if current == nil || current.ID != finding.CueID || current.CueRevision != finding.ResolvedCueRevision || !finding.EvidenceValid {
		result.Status = "stale"
		return result
	}
	result.Valid = true
	result.Status = "valid"
	return result
}
