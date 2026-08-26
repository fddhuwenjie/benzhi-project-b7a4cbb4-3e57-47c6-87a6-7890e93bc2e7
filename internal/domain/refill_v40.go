package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const MinimumCueDurationMS int64 = 200

type CueSplitPreview struct {
	ProjectID         string     `json:"project_id"`
	ProjectRevision   int64      `json:"project_revision"`
	SourceCueID       string     `json:"source_cue_id"`
	SplitTimeMS       int64      `json:"split_time_ms"`
	TextOffset        int        `json:"text_offset"`
	First             CaptionCue `json:"first"`
	Second            CaptionCue `json:"second"`
	SoundAssignment   string     `json:"sound_assignment"`
	MinimumDurationMS int64      `json:"minimum_duration_ms"`
}

type CueSplitResult struct {
	SourceCueID string          `json:"source_cue_id"`
	NewCueID    string          `json:"new_cue_id"`
	SplitTimeMS int64           `json:"split_time_ms"`
	First       CaptionCue      `json:"first"`
	Second      CaptionCue      `json:"second"`
	Preview     CueSplitPreview `json:"preview"`
}

// PreviewCueSplit performs the same timeline validation as confirmation but
// returns detached cue values and never changes the aggregate.
func (p *CaptionProject) PreviewCueSplit(cueID string, splitTimeMS int64, textOffset int, expectedRevision int64) (*CueSplitPreview, error) {
	if p.Status == StatusReleased {
		return nil, Forbidden("已发布的字幕母版不可拆分")
	}
	if p.Status != StatusDraft && p.Status != StatusChanges {
		return nil, Conflict("仅草稿或退回整改状态可拆分字幕段")
	}
	if expectedRevision <= 0 {
		return nil, Invalid("expected_revision 必须大于零", "expected_revision")
	}
	if expectedRevision != p.Revision {
		return nil, ConflictWithDetails("拆分预检修订已失效", map[string]any{"current_revision": p.Revision, "expected_revision": expectedRevision})
	}
	cueID = strings.TrimSpace(cueID)
	if cueID == "" {
		return nil, Invalid("字幕段 ID 不能为空", "cue_id")
	}
	source := p.findCue(cueID)
	if source == nil {
		return nil, NotFound("字幕段", cueID)
	}
	if splitTimeMS <= source.StartMS || splitTimeMS >= source.EndMS {
		return nil, Invalid("拆分时间必须严格位于原字幕段内部", "split_time_ms")
	}
	runes := []rune(source.Text)
	if textOffset <= 0 || textOffset >= len(runes) {
		return nil, Invalid("正文拆分位置必须严格位于正文内部", "text_offset")
	}
	firstText, secondText := strings.TrimSpace(string(runes[:textOffset])), strings.TrimSpace(string(runes[textOffset:]))
	if firstText == "" || secondText == "" {
		return nil, Invalid("正文拆分位置必须产生两段非空文本", "text_offset")
	}
	first := *source
	first.EndMS, first.Text = splitTimeMS, firstText
	second := *source
	second.ID = ""
	second.Sequence = source.Sequence + 1
	second.StartMS, second.Text = splitTimeMS, secondText
	second.SoundDescription = ""
	second.CueRevision = 1
	if first.EndMS-first.StartMS < MinimumCueDurationMS || second.EndMS-second.StartMS < MinimumCueDurationMS {
		return nil, Invalid(fmt.Sprintf("拆分后的每段字幕至少持续 %dms", MinimumCueDurationMS), "split_time_ms")
	}
	candidate := make([]CaptionCue, 0, len(p.Cues)+1)
	for _, cue := range p.Cues {
		if cue.ID != source.ID {
			candidate = append(candidate, cue)
			continue
		}
		candidate = append(candidate, first, second)
	}
	if err := validateSplitTimeline(candidate, p.DurationMS); err != nil {
		return nil, err
	}
	return &CueSplitPreview{
		ProjectID: p.ID, ProjectRevision: p.Revision, SourceCueID: source.ID,
		SplitTimeMS: splitTimeMS, TextOffset: textOffset, First: first, Second: second,
		SoundAssignment: "first", MinimumDurationMS: MinimumCueDurationMS,
	}, nil
}

func (p *CaptionProject) ApplyCueSplit(cueID, newCueID string, splitTimeMS int64, textOffset int, previewRevision int64, now time.Time) (*CueSplitResult, error) {
	if previewRevision != p.Revision {
		return nil, ConflictWithDetails("拆分预检修订已失效", map[string]any{"current_revision": p.Revision, "preview_revision": previewRevision})
	}
	preview, err := p.PreviewCueSplit(cueID, splitTimeMS, textOffset, previewRevision)
	if err != nil {
		return nil, err
	}
	newCueID = strings.TrimSpace(newCueID)
	if newCueID == "" {
		return nil, Invalid("新字幕段 ID 不能为空", "new_cue_id")
	}
	if p.findCue(newCueID) != nil {
		return nil, Conflict("新字幕段 ID 已存在")
	}
	updated := make([]CaptionCue, 0, len(p.Cues)+1)
	for _, current := range p.Cues {
		if current.ID != preview.SourceCueID {
			if current.Sequence > preview.First.Sequence {
				current.Sequence++
				current.CueRevision++
			}
			updated = append(updated, current)
			continue
		}
		first := preview.First
		first.CueRevision = current.CueRevision + 1
		second := preview.Second
		second.ID, second.ProjectID = newCueID, p.ID
		updated = append(updated, first, second)
	}
	p.Cues = updated
	p.Checks = []RuleCheck{}
	p.refreshEvidence()
	p.UpdatedAt = now.UTC()
	result := &CueSplitResult{SourceCueID: preview.SourceCueID, NewCueID: newCueID, SplitTimeMS: splitTimeMS, First: p.Cues[preview.First.Sequence-1], Second: p.Cues[preview.First.Sequence], Preview: *preview}
	result.Preview.First, result.Preview.Second = result.First, result.Second
	return result, nil
}

func validateSplitTimeline(cues []CaptionCue, durationMS int64) error {
	previousEnd := int64(-1)
	for index, cue := range cues {
		if cue.StartMS < 0 || cue.EndMS > durationMS || cue.EndMS <= cue.StartMS {
			return Invalid(fmt.Sprintf("字幕段 %s 超出节目时间边界", cue.ID), "split_time_ms")
		}
		if cue.EndMS-cue.StartMS < MinimumCueDurationMS {
			return Invalid(fmt.Sprintf("字幕段 %s 持续时间少于 %dms", cue.ID, MinimumCueDurationMS), "split_time_ms")
		}
		if index > 0 && previousEnd > cue.StartMS {
			return Invalid(fmt.Sprintf("字幕段 %s 与相邻字幕重叠", cue.ID), "split_time_ms")
		}
		previousEnd = cue.EndMS
	}
	return nil
}

type FindingWorklistQuery struct {
	Statuses         []FindingStatus
	Severities       []string
	Categories       []string
	CueID            string
	Keyword          string
	Sort             string
	ExpectedRevision int64
}

type FindingWorkItem struct {
	Finding        ReviewFinding `json:"finding"`
	CueSequence    int           `json:"cue_sequence"`
	StartMS        int64         `json:"start_ms"`
	EndMS          int64         `json:"end_ms"`
	TextSummary    string        `json:"text_summary"`
	CueRevision    int64         `json:"cue_revision"`
	EvidenceStatus string        `json:"evidence_status"`
	LastVerifiedAt *time.Time    `json:"last_verified_at,omitempty"`
	AllowedActions []string      `json:"allowed_actions"`
}

type FindingWorklistStats struct {
	MatchedCount         int                   `json:"matched_count"`
	StatusCounts         map[FindingStatus]int `json:"status_counts"`
	SeverityCounts       map[string]int        `json:"severity_counts"`
	CategoryCounts       map[string]int        `json:"category_counts"`
	DistinctCueCount     int                   `json:"distinct_cue_count"`
	WithoutEvidenceCount int                   `json:"without_evidence_count"`
	StaleEvidenceCount   int                   `json:"stale_evidence_count"`
}

type FindingWorklist struct {
	ProjectID          string               `json:"project_id"`
	ProjectRevision    int64                `json:"project_revision"`
	ExpectedRevision   int64                `json:"expected_revision"`
	RevisionMatches    bool                 `json:"revision_matches"`
	BulkActionsAllowed bool                 `json:"bulk_actions_allowed"`
	Items              []FindingWorkItem    `json:"items"`
	Stats              FindingWorklistStats `json:"stats"`
}

func QueryFindingWorklist(p *CaptionProject, query FindingWorklistQuery) (*FindingWorklist, error) {
	if query.ExpectedRevision <= 0 {
		return nil, Invalid("expected_revision 必须大于零", "expected_revision")
	}
	if utf8.RuneCountInString(strings.TrimSpace(query.Keyword)) > 100 {
		return nil, Invalid("关键词长度不能超过 100", "keyword")
	}
	statusSet := map[FindingStatus]bool{}
	for _, value := range query.Statuses {
		if value != FindingStatus("unclosed") && !validFindingStatus(value) {
			return nil, Invalid("未知问题状态", "status")
		}
		statusSet[value] = true
	}
	severitySet := map[string]bool{}
	for _, value := range query.Severities {
		if value != "minor" && value != "major" && value != "critical" {
			return nil, Invalid("未知严重级别", "severity")
		}
		severitySet[value] = true
	}
	categorySet := map[string]bool{}
	for _, value := range query.Categories {
		if !supportedFindingCategory(value) {
			return nil, Invalid("未知规则分类", "category")
		}
		categorySet[value] = true
	}
	sortName := strings.TrimSpace(query.Sort)
	if sortName == "" {
		sortName = "severity_desc"
	}
	if sortName != "severity_desc" && sortName != "timeline_asc" && sortName != "verified_desc" {
		return nil, Invalid("未知排序字段", "sort")
	}
	cues := map[string]CaptionCue{}
	for _, cue := range p.Cues {
		cues[cue.ID] = cue
	}
	keyword := compactFold(query.Keyword)
	items := []FindingWorkItem{}
	for _, finding := range p.Findings {
		if len(statusSet) > 0 && !statusSet[finding.Status] && !(statusSet[FindingStatus("unclosed")] && finding.Status != FindingResolved) {
			continue
		}
		if len(severitySet) > 0 && !severitySet[finding.Severity] || len(categorySet) > 0 && !categorySet[finding.Category] || query.CueID != "" && finding.CueID != query.CueID {
			continue
		}
		if keyword != "" && !strings.Contains(compactFold(finding.Description), keyword) {
			continue
		}
		cue, exists := cues[finding.CueID]
		if !exists {
			continue
		}
		evidenceStatus := "missing"
		if finding.ResolvedCueRevision > 0 {
			evidenceStatus = "stale"
			if finding.EvidenceValid {
				evidenceStatus = "valid"
			}
		}
		item := FindingWorkItem{Finding: finding, CueSequence: cue.Sequence, StartMS: cue.StartMS, EndMS: cue.EndMS, TextSummary: summarizeText(cue.Text, cue.SoundDescription, 80), CueRevision: cue.CueRevision, EvidenceStatus: evidenceStatus, LastVerifiedAt: finding.VerifiedAt, AllowedActions: findingActions(p.Status, finding)}
		items = append(items, item)
	}
	severityRank := map[string]int{"critical": 3, "major": 2, "minor": 1}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch sortName {
		case "timeline_asc":
			if a.StartMS != b.StartMS {
				return a.StartMS < b.StartMS
			}
			if a.CueSequence != b.CueSequence {
				return a.CueSequence < b.CueSequence
			}
		case "verified_desc":
			if a.LastVerifiedAt != nil || b.LastVerifiedAt != nil {
				if a.LastVerifiedAt == nil {
					return false
				}
				if b.LastVerifiedAt == nil {
					return true
				}
				if !a.LastVerifiedAt.Equal(*b.LastVerifiedAt) {
					return a.LastVerifiedAt.After(*b.LastVerifiedAt)
				}
			}
		default:
			if severityRank[a.Finding.Severity] != severityRank[b.Finding.Severity] {
				return severityRank[a.Finding.Severity] > severityRank[b.Finding.Severity]
			}
			if a.StartMS != b.StartMS {
				return a.StartMS < b.StartMS
			}
		}
		return a.Finding.ID < b.Finding.ID
	})
	stats := FindingWorklistStats{StatusCounts: map[FindingStatus]int{}, SeverityCounts: map[string]int{}, CategoryCounts: map[string]int{}}
	cueSet := map[string]bool{}
	for _, item := range items {
		stats.MatchedCount++
		stats.StatusCounts[item.Finding.Status]++
		stats.SeverityCounts[item.Finding.Severity]++
		stats.CategoryCounts[item.Finding.Category]++
		cueSet[item.Finding.CueID] = true
		if item.EvidenceStatus == "missing" {
			stats.WithoutEvidenceCount++
		}
		if item.EvidenceStatus == "stale" {
			stats.StaleEvidenceCount++
		}
	}
	stats.DistinctCueCount = len(cueSet)
	revisionMatches := query.ExpectedRevision == p.Revision
	return &FindingWorklist{ProjectID: p.ID, ProjectRevision: p.Revision, ExpectedRevision: query.ExpectedRevision, RevisionMatches: revisionMatches, BulkActionsAllowed: revisionMatches && (p.Status == StatusChanges || p.Status == StatusReverification), Items: items, Stats: stats}, nil
}

func findingActions(status ProjectStatus, finding ReviewFinding) []string {
	actions := []string{"view", "locate_cue"}
	if status == StatusChanges && (finding.Status == FindingOpen || finding.Status == FindingRejected || finding.Status == FindingResolved && !finding.EvidenceValid) {
		return append(actions, "remediate")
	}
	if status == StatusReverification && finding.Status == FindingRemediated && finding.EvidenceValid {
		return append(actions, "verify")
	}
	return actions
}

func summarizeText(text, sound string, limit int) string {
	value := strings.TrimSpace(text)
	if value == "" {
		value = strings.TrimSpace(sound)
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

type VerificationMediaBaseline struct {
	Title         string `json:"title"`
	DurationMS    int64  `json:"duration_ms"`
	Language      string `json:"language"`
	StyleProfile  string `json:"style_profile"`
	MediaChecksum string `json:"media_checksum"`
}

type VerificationApproval struct {
	ApprovedBy   string    `json:"approved_by"`
	ApprovedAt   time.Time `json:"approved_at"`
	AuditEventID int64     `json:"audit_event_id"`
}

type VerificationPackage struct {
	FormatVersion   string                    `json:"format_version"`
	ProjectID       string                    `json:"project_id"`
	ProjectRevision int64                     `json:"project_revision"`
	ManifestID      string                    `json:"manifest_id"`
	ManifestVersion string                    `json:"manifest_version"`
	MediaBaseline   VerificationMediaBaseline `json:"media_baseline"`
	Captions        []CaptionCue              `json:"captions"`
	Approval        VerificationApproval      `json:"approval"`
	CueCount        int                       `json:"cue_count"`
	CaptionChecksum string                    `json:"caption_checksum"`
	MediaChecksum   string                    `json:"media_checksum"`
}

type VerificationPackageSummary struct {
	ProjectID       string            `json:"project_id"`
	ProjectRevision int64             `json:"project_revision"`
	ManifestID      string            `json:"manifest_id"`
	ManifestVersion string            `json:"manifest_version"`
	FormatVersion   string            `json:"format_version"`
	CueCount        int               `json:"cue_count"`
	CaptionChecksum string            `json:"caption_checksum"`
	MediaChecksum   string            `json:"media_checksum"`
	Integrity       ManifestIntegrity `json:"integrity"`
	DownloadReady   bool              `json:"download_ready"`
}

func BuildVerificationPackage(project *CaptionProject, manifest *ReleaseManifest, frozen []CaptionCue, snapshotChecksum string, approval *AuditEvent) (*VerificationPackage, ManifestIntegrity) {
	integrity := ManifestIntegrity{Complete: true, Checks: []IntegrityItem{}}
	add := func(name string, passed bool, reason string, expected, actual any) {
		integrity.Checks = append(integrity.Checks, IntegrityItem{Name: name, Passed: passed, Reason: reason, Expected: expected, Actual: actual})
		if !passed {
			integrity.Complete = false
		}
	}
	if manifest == nil {
		add("manifest", false, "发布清单不存在", "已发布清单", nil)
		return nil, integrity
	}
	ordered := append([]CaptionCue(nil), frozen...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	for i := range ordered {
		ordered[i].Speaker = strings.TrimSpace(ordered[i].Speaker)
		ordered[i].Text = strings.TrimSpace(ordered[i].Text)
		ordered[i].SoundDescription = strings.TrimSpace(ordered[i].SoundDescription)
	}
	frozenProject := *project
	frozenProject.Cues = ordered
	computedChecksum := frozenProject.CaptionChecksum()
	timelineError := validateFrozenSnapshot(project.ID, ordered, project.DurationMS)
	add("project_status", project.Status == StatusReleased, "项目必须已发布", StatusReleased, project.Status)
	add("project_id", manifest.ProjectID == project.ID, "清单项目标识", project.ID, manifest.ProjectID)
	add("project_revision", manifest.ProjectRevision == project.Revision, "清单冻结修订", project.Revision, manifest.ProjectRevision)
	add("frozen_timeline", timelineError == nil, "冻结字幕结构与节目边界", "有效且连续排序的冻结字幕", errorText(timelineError))
	add("cue_count", manifest.CueCount == len(ordered), "冻结字幕数量", manifest.CueCount, len(ordered))
	add("caption_checksum", manifest.CaptionChecksum == computedChecksum, "规范化冻结字幕内容校验", manifest.CaptionChecksum, computedChecksum)
	add("snapshot_checksum", snapshotChecksum == computedChecksum, "存储快照校验值", snapshotChecksum, computedChecksum)
	add("media_checksum", manifest.MediaChecksum == project.MediaChecksum, "素材基线校验值", manifest.MediaChecksum, project.MediaChecksum)
	add("manifest_version", strings.TrimSpace(manifest.ManifestVersion) != "", "清单版本", "非空", manifest.ManifestVersion)
	if approval == nil {
		add("approval_audit", false, "批准审计事件不存在", "release.approved", nil)
	} else {
		add("approval_type", approval.Type == "release.approved", "批准审计事件类型", "release.approved", approval.Type)
		add("approval_actor", approval.Actor == manifest.ApprovedBy, "批准人与审计事件", manifest.ApprovedBy, approval.Actor)
		add("approval_revision", approval.Revision == manifest.ProjectRevision, "批准修订与审计事件", manifest.ProjectRevision, approval.Revision)
		add("approval_time", approval.CreatedAt.Equal(manifest.ApprovedAt), "批准时间与审计事件", manifest.ApprovedAt, approval.CreatedAt)
	}
	pack := &VerificationPackage{FormatVersion: "1", ProjectID: project.ID, ProjectRevision: manifest.ProjectRevision, ManifestID: manifest.ID, ManifestVersion: manifest.ManifestVersion, MediaBaseline: VerificationMediaBaseline{Title: project.Title, DurationMS: project.DurationMS, Language: project.Language, StyleProfile: project.StyleProfile, MediaChecksum: project.MediaChecksum}, Captions: ordered, CueCount: len(ordered), CaptionChecksum: computedChecksum, MediaChecksum: project.MediaChecksum}
	if approval != nil {
		pack.Approval = VerificationApproval{ApprovedBy: manifest.ApprovedBy, ApprovedAt: manifest.ApprovedAt, AuditEventID: approval.ID}
	}
	return pack, integrity
}

func validateFrozenSnapshot(projectID string, cues []CaptionCue, durationMS int64) error {
	seen := map[string]bool{}
	previousEnd := int64(-1)
	for index, cue := range cues {
		if strings.TrimSpace(cue.ID) == "" || cue.ProjectID != projectID || seen[cue.ID] {
			return fmt.Errorf("第 %d 段标识或项目引用无效", index+1)
		}
		if cue.Sequence != index+1 {
			return fmt.Errorf("字幕段 %s 序号不连续", cue.ID)
		}
		if cue.StartMS < 0 || cue.EndMS <= cue.StartMS || cue.EndMS > durationMS || previousEnd > cue.StartMS {
			return fmt.Errorf("字幕段 %s 时间边界或重叠关系无效", cue.ID)
		}
		if strings.TrimSpace(cue.Text) == "" && strings.TrimSpace(cue.SoundDescription) == "" {
			return fmt.Errorf("字幕段 %s 内容为空", cue.ID)
		}
		seen[cue.ID], previousEnd = true, cue.EndMS
	}
	return nil
}

func errorText(err error) any {
	if err == nil {
		return "有效"
	}
	return err.Error()
}
