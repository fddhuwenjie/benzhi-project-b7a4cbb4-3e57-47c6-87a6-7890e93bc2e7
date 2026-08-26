package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type NewProject struct {
	ID            string
	Title         string
	DurationMS    int64
	Language      string
	MediaChecksum string
	StyleProfile  string
	Assignee      string
}

func CreateProject(input NewProject, now time.Time) (*CaptionProject, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.Title = strings.TrimSpace(input.Title)
	input.Language = strings.TrimSpace(input.Language)
	input.MediaChecksum = strings.ToLower(input.MediaChecksum)
	input.StyleProfile = strings.TrimSpace(input.StyleProfile)
	input.Assignee = strings.TrimSpace(input.Assignee)
	var fields []string
	if input.ID == "" {
		fields = append(fields, "id")
	}
	if input.Title == "" {
		fields = append(fields, "title")
	}
	if input.DurationMS <= 0 {
		fields = append(fields, "duration_ms")
	}
	if input.Language == "" {
		fields = append(fields, "language")
	}
	if !validSHA256(input.MediaChecksum) {
		fields = append(fields, "media_checksum")
	}
	if input.StyleProfile == "" {
		fields = append(fields, "style_profile")
	}
	if input.Assignee == "" {
		fields = append(fields, "assignee")
	}
	if len(fields) > 0 {
		return nil, Invalid("节目素材基线字段不完整", fields...)
	}
	return &CaptionProject{
		ID: input.ID, Title: input.Title, DurationMS: input.DurationMS,
		Language: input.Language, MediaChecksum: input.MediaChecksum,
		StyleProfile: input.StyleProfile, Assignee: input.Assignee,
		Status: StatusDraft, Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
		Cues: []CaptionCue{}, Checks: []RuleCheck{}, CheckRuns: []RuleCheckRun{}, Findings: []ReviewFinding{},
	}, nil
}

func (p *CaptionProject) SaveCues(cues []CaptionCue, now time.Time) error {
	if p.Status == StatusReleased {
		return Forbidden("已发布的字幕母版不可编辑")
	}
	if p.Status != StatusDraft && p.Status != StatusChanges {
		return Conflict("当前状态不允许编辑字幕")
	}
	if len(cues) == 0 {
		return Invalid("至少需要一个字幕段", "cues")
	}
	seen := map[string]bool{}
	copyCues := append([]CaptionCue(nil), cues...)
	sort.SliceStable(copyCues, func(i, j int) bool { return copyCues[i].StartMS < copyCues[j].StartMS })
	for i := range copyCues {
		cue := &copyCues[i]
		cue.ID = strings.TrimSpace(cue.ID)
		cue.Text = strings.TrimSpace(cue.Text)
		cue.Speaker = strings.TrimSpace(cue.Speaker)
		cue.SoundDescription = strings.TrimSpace(cue.SoundDescription)
		if cue.ID == "" {
			return Invalid("字幕段 ID 不能为空", "cues.id")
		}
		if seen[cue.ID] {
			return Invalid("字幕段 ID 重复", "cues.id")
		}
		seen[cue.ID] = true
		if cue.StartMS < 0 || cue.EndMS <= cue.StartMS || cue.EndMS > p.DurationMS {
			return Invalid(fmt.Sprintf("字幕段 %s 超出节目时间边界", cue.ID), "cues.start_ms", "cues.end_ms")
		}
		if cue.Text == "" && cue.SoundDescription == "" {
			return Invalid(fmt.Sprintf("字幕段 %s 内容为空", cue.ID), "cues.text")
		}
		if i > 0 && copyCues[i-1].EndMS > cue.StartMS {
			return Invalid(fmt.Sprintf("字幕段 %s 与前一段重叠", cue.ID), "cues.start_ms")
		}
		cue.ProjectID = p.ID
		cue.Sequence = i + 1
		oldRevision := int64(0)
		var oldCue *CaptionCue
		for _, old := range p.Cues {
			if old.ID == cue.ID {
				oldRevision = old.CueRevision
				copyOld := old
				oldCue = &copyOld
			}
		}
		cue.CueRevision = oldRevision
		if oldCue == nil || cueContentChanged(*oldCue, *cue) {
			cue.CueRevision++
		}
	}
	for _, finding := range p.Findings {
		if !seen[finding.CueID] {
			return Invalid(fmt.Sprintf("审校问题 %s 引用的字幕段不可删除", finding.ID), "cues")
		}
	}
	p.Cues = copyCues
	p.Checks = []RuleCheck{}
	p.refreshEvidence()
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *CaptionProject) RunChecks(now time.Time) []RuleCheck {
	return p.RunChecksForRevision(fmt.Sprintf("run-%d", now.UTC().UnixNano()), p.Revision, now)
}

func (p *CaptionProject) RunChecksForRevision(runID string, revision int64, now time.Time) []RuleCheck {
	checks := EvaluateAccessibility(p.Cues, DefaultAccessibilityPolicy(), now)
	runID = strings.TrimSpace(runID)
	for i := range checks {
		checks[i].ID = fmt.Sprintf("%s-result-%d", runID, i+1)
	}
	p.Checks = checks
	p.CheckRuns = append(p.CheckRuns, RuleCheckRun{ID: runID, ProjectRevision: revision, RunAt: now.UTC(), Results: append([]RuleCheck(nil), checks...)})
	p.UpdatedAt = now.UTC()
	return append([]RuleCheck(nil), checks...)
}

func (p *CaptionProject) ChecksPassed() bool {
	if len(p.Checks) == 0 {
		return false
	}
	for _, check := range p.Checks {
		if !check.Passed && check.Level == "error" {
			return false
		}
	}
	return true
}

func (p *CaptionProject) CurrentChecksPassed() bool {
	if !p.HasCurrentChecks() {
		return false
	}
	return p.ChecksPassed()
}

func (p *CaptionProject) HasCurrentChecks() bool {
	return len(p.CheckRuns) > 0 && len(p.Checks) > 0
}

func (p *CaptionProject) SubmitReview(now time.Time) error {
	if p.Status != StatusDraft {
		return Conflict("仅草稿状态可提交审校")
	}
	if !p.HasCurrentChecks() {
		return Invalid("当前字幕版本尚未执行规则检查", "checks")
	}
	p.Status = StatusInReview
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *CaptionProject) AddFinding(f ReviewFinding, now time.Time) error {
	return p.AddFindings([]ReviewFinding{f}, now)
}

func (p *CaptionProject) AddFindings(findings []ReviewFinding, now time.Time) error {
	if p.Status != StatusInReview {
		return Conflict("仅审校中可登记问题")
	}
	if len(findings) == 0 {
		return Invalid("至少需要登记一个审校问题", "findings")
	}
	prepared := make([]ReviewFinding, len(findings))
	batchKeys := map[string]int{}
	for index, input := range findings {
		f := input
		f.ID, f.CueID = strings.TrimSpace(f.ID), strings.TrimSpace(f.CueID)
		f.Category, f.Severity = strings.TrimSpace(f.Category), strings.TrimSpace(f.Severity)
		f.Description, f.ReportedBy = normalizeDescription(f.Description), strings.TrimSpace(f.ReportedBy)
		field := fmt.Sprintf("findings[%d]", index)
		if f.ID == "" || f.CueID == "" || f.Category == "" || f.Description == "" || f.ReportedBy == "" {
			return Invalid(fmt.Sprintf("第 %d 项审校问题字段不完整", index+1), field)
		}
		if !supportedFindingCategory(f.Category) {
			return Invalid(fmt.Sprintf("第 %d 项规则分类不受支持", index+1), field+".category")
		}
		if f.Severity != "minor" && f.Severity != "major" && f.Severity != "critical" {
			return Invalid(fmt.Sprintf("第 %d 项严重级别无效", index+1), field+".severity")
		}
		cue := p.findCue(f.CueID)
		if cue == nil {
			return Invalid(fmt.Sprintf("第 %d 项引用不存在的字幕段 %s", index+1, f.CueID), field+".cue_id")
		}
		key := findingDuplicateKey(f)
		if first, exists := batchKeys[key]; exists {
			return ConflictWithDetails("批次内存在重复审校问题", map[string]any{"duplicate_index": index, "first_index": first})
		}
		batchKeys[key] = index
		for _, existing := range p.Findings {
			if existing.ID == f.ID {
				return ConflictWithDetails("审校问题 ID 已存在", map[string]any{"duplicate_index": index, "existing_finding_id": existing.ID})
			}
			if existing.Status != FindingResolved && findingDuplicateKey(existing) == key {
				return ConflictWithDetails("已存在相同的未关闭审校问题", map[string]any{"duplicate_index": index, "existing_finding_id": existing.ID})
			}
		}
		f.ProjectID, f.Status, f.ReportedCueRevision = p.ID, FindingOpen, cue.CueRevision
		f.ReviewHistory = []FindingReview{}
		prepared[index] = f
	}
	p.Findings = append(p.Findings, prepared...)
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *CaptionProject) ReviewDecision(approved bool, now time.Time) error {
	if p.Status != StatusInReview {
		return Conflict("当前状态不可作出审校决定")
	}
	if approved {
		if p.hasUnresolvedFindings() {
			return Invalid("仍有未关闭审校问题", "findings")
		}
		p.Status = StatusReady
	} else {
		if len(p.Findings) == 0 || !p.hasUnresolvedFindings() {
			return Invalid("退回前必须登记至少一个未解决问题", "findings")
		}
		p.Status = StatusChanges
	}
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *CaptionProject) Remediate(findingID, note string, now time.Time) error {
	if p.Status != StatusChanges {
		return Conflict("仅整改状态可记录修订说明")
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return Invalid("整改说明不能为空", "resolution_note")
	}
	f := p.findFinding(findingID)
	if f == nil {
		return NotFound("审校问题", findingID)
	}
	if f.Status != FindingOpen && f.Status != FindingRejected && !(f.Status == FindingResolved && !f.EvidenceValid) {
		return Conflict("该问题当前不可整改")
	}
	cue := p.findCue(f.CueID)
	if cue == nil {
		return NotFound("字幕段", f.CueID)
	}
	minimumRevision := f.ReportedCueRevision
	if f.ResolvedCueRevision > minimumRevision {
		minimumRevision = f.ResolvedCueRevision
	}
	if cue.CueRevision <= minimumRevision {
		return Invalid("必须先修改对应字幕并形成更高字幕版本，才能记录整改证据", "finding_id")
	}
	f.Status, f.ResolutionNote, f.ResolvedCueRevision, f.EvidenceValid = FindingRemediated, note, cue.CueRevision, true
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *CaptionProject) SubmitReverification(now time.Time) error {
	if p.Status != StatusChanges {
		return Conflict("仅整改状态可提交定向复验")
	}
	if !p.CurrentChecksPassed() {
		return Invalid("修改后必须重新执行并通过规则检查", "checks")
	}
	for _, f := range p.Findings {
		if f.Status != FindingRemediated && f.Status != FindingResolved {
			return Invalid("所有待处理问题必须先完成整改", "findings")
		}
		if !f.EvidenceValid {
			return Invalid(fmt.Sprintf("问题 %s 的整改证据已过期", f.ID), "findings")
		}
	}
	p.Status = StatusReverification
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *CaptionProject) VerifyFinding(id, reviewer string, resolved bool, now time.Time) error {
	if p.Status != StatusReverification {
		return Conflict("仅定向复验状态可确认问题")
	}
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return Invalid("复验人不能为空", "verified_by")
	}
	f := p.findFinding(id)
	if f == nil {
		return NotFound("审校问题", id)
	}
	if f.Status != FindingRemediated {
		return Conflict("仅已整改问题可复验")
	}
	if resolved {
		f.Status = FindingResolved
	} else {
		f.Status = FindingRejected
	}
	t := now.UTC()
	f.VerifiedBy, f.VerifiedAt = reviewer, &t
	f.ReviewHistory = append(f.ReviewHistory, FindingReview{Reviewer: reviewer, Resolved: resolved, CueRevision: f.ResolvedCueRevision, ReviewedAt: t})
	if !resolved {
		f.EvidenceValid = false
		p.Status = StatusChanges
	}
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *CaptionProject) CompleteReverification(now time.Time) error {
	if p.Status != StatusReverification {
		return Conflict("当前状态不可完成复验")
	}
	for _, f := range p.Findings {
		if f.Status != FindingResolved {
			return Invalid("仍有问题未通过复验", "findings")
		}
	}
	p.Status, p.UpdatedAt = StatusReady, now.UTC()
	return nil
}

func (p *CaptionProject) Approve(approver, manifestID string, now time.Time) (*ReleaseManifest, error) {
	preview := p.ReleasePreview()
	return p.ApprovePreview(approver, manifestID, preview.CurrentRevision, preview.CaptionChecksum, preview.ConfirmationToken, now)
}

func (p *CaptionProject) ApprovePreview(approver, manifestID string, previewRevision int64, previewChecksum, confirmationToken string, now time.Time) (*ReleaseManifest, error) {
	if p.Status != StatusReady {
		return nil, Conflict("仅待发布状态可批准发布")
	}
	preview := p.ReleasePreview()
	if len(preview.Blockers) > 0 {
		return nil, Invalid("发布预检存在阻断项", "preflight")
	}
	if previewRevision != preview.CurrentRevision || !strings.EqualFold(strings.TrimSpace(previewChecksum), preview.CaptionChecksum) || confirmationToken != preview.ConfirmationToken {
		return nil, Conflict("发布预览已失效，请重新预检")
	}
	approver, manifestID = strings.TrimSpace(approver), strings.TrimSpace(manifestID)
	if approver == "" || manifestID == "" {
		return nil, Invalid("批准人和清单 ID 不能为空", "approved_by")
	}
	manifest := &ReleaseManifest{ID: manifestID, ProjectID: p.ID, ProjectRevision: p.Revision + 1, CueCount: len(p.Cues), CaptionChecksum: p.CaptionChecksum(), MediaChecksum: p.MediaChecksum, ApprovedBy: approver, ApprovedAt: now.UTC(), ManifestVersion: "1"}
	p.Status, p.Manifest, p.UpdatedAt = StatusReleased, manifest, now.UTC()
	return manifest, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func cueContentChanged(a, b CaptionCue) bool {
	return a.StartMS != b.StartMS || a.EndMS != b.EndMS || strings.TrimSpace(a.Speaker) != b.Speaker || strings.TrimSpace(a.Text) != b.Text || strings.TrimSpace(a.SoundDescription) != b.SoundDescription
}

func normalizeDescription(value string) string { return strings.Join(strings.Fields(value), " ") }
func findingDuplicateKey(f ReviewFinding) string {
	return f.CueID + "\x00" + strings.ToLower(f.Category) + "\x00" + strings.ToLower(normalizeDescription(f.Description))
}
func supportedFindingCategory(value string) bool {
	switch value {
	case "accuracy", "accessibility", "timing", "style", "reading_speed", "speaker", "sound_description", "caption_gap":
		return true
	default:
		return false
	}
}

func (p *CaptionProject) refreshEvidence() {
	for i := range p.Findings {
		f := &p.Findings[i]
		cue := p.findCue(f.CueID)
		f.EvidenceValid = f.ResolvedCueRevision > 0 && cue != nil && cue.CueRevision == f.ResolvedCueRevision && f.Status != FindingRejected
	}
}

func (p *CaptionProject) CaptionChecksum() string {
	type normalized struct {
		Sequence int    `json:"sequence"`
		StartMS  int64  `json:"start_ms"`
		EndMS    int64  `json:"end_ms"`
		Speaker  string `json:"speaker"`
		Text     string `json:"text"`
		Sound    string `json:"sound_description"`
	}
	rows := make([]normalized, len(p.Cues))
	for i, c := range p.Cues {
		rows[i] = normalized{c.Sequence, c.StartMS, c.EndMS, strings.TrimSpace(c.Speaker), strings.TrimSpace(c.Text), strings.TrimSpace(c.SoundDescription)}
	}
	b, _ := json.Marshal(rows)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (p *CaptionProject) findCue(id string) *CaptionCue {
	for i := range p.Cues {
		if p.Cues[i].ID == id {
			return &p.Cues[i]
		}
	}
	return nil
}
func (p *CaptionProject) findFinding(id string) *ReviewFinding {
	for i := range p.Findings {
		if p.Findings[i].ID == id {
			return &p.Findings[i]
		}
	}
	return nil
}
func (p *CaptionProject) hasUnresolvedFindings() bool {
	for _, f := range p.Findings {
		if f.Status != FindingResolved {
			return true
		}
	}
	return false
}
