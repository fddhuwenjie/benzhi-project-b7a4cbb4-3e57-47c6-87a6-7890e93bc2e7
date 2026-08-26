package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (p *CaptionProject) PreviewCueShift(cueIDs []string, offsetMS int64) (*CueShiftPreview, error) {
	if p.Status != StatusDraft && p.Status != StatusChanges {
		return nil, Conflict("仅草稿或退回整改状态可批量偏移字幕")
	}
	if offsetMS == 0 {
		return nil, Invalid("偏移量不能为零", "offset_ms")
	}
	if len(cueIDs) == 0 {
		return nil, Invalid("至少选择一个字幕段", "cue_ids")
	}
	selected := make(map[string]bool, len(cueIDs))
	for _, raw := range cueIDs {
		id := strings.TrimSpace(raw)
		if id == "" || selected[id] {
			return nil, Invalid("所选字幕段标识为空或重复", "cue_ids")
		}
		if p.findCue(id) == nil {
			return nil, NotFound("字幕段", id)
		}
		selected[id] = true
	}
	shifted := append([]CaptionCue(nil), p.Cues...)
	changes := make([]CueShiftChange, 0, len(selected))
	for i := range shifted {
		if !selected[shifted[i].ID] {
			continue
		}
		oldStart, oldEnd := shifted[i].StartMS, shifted[i].EndMS
		shifted[i].StartMS += offsetMS
		shifted[i].EndMS += offsetMS
		changes = append(changes, CueShiftChange{CueID: shifted[i].ID, OldStartMS: oldStart, OldEndMS: oldEnd, NewStartMS: shifted[i].StartMS, NewEndMS: shifted[i].EndMS})
	}
	sort.SliceStable(shifted, func(i, j int) bool { return shifted[i].StartMS < shifted[j].StartMS })
	for i, cue := range shifted {
		if cue.StartMS < 0 || cue.EndMS > p.DurationMS {
			return nil, Invalid(fmt.Sprintf("字幕段 %s 偏移后超出节目时间边界", cue.ID), "offset_ms")
		}
		if i > 0 && shifted[i-1].EndMS > cue.StartMS {
			return nil, Invalid(fmt.Sprintf("字幕段 %s 偏移后与相邻字幕重叠", cue.ID), "offset_ms")
		}
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].OldStartMS < changes[j].OldStartMS })
	return &CueShiftPreview{ProjectRevision: p.Revision, OffsetMS: offsetMS, Changes: changes}, nil
}

func (p *CaptionProject) ApplyCueShift(cueIDs []string, offsetMS int64, previewRevision int64, now time.Time) (*CueShiftPreview, error) {
	if previewRevision != p.Revision {
		return nil, Conflict(fmt.Sprintf("批量偏移预览已失效：当前为 %d，预览为 %d", p.Revision, previewRevision))
	}
	preview, err := p.PreviewCueShift(cueIDs, offsetMS)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(cueIDs))
	for _, id := range cueIDs {
		selected[strings.TrimSpace(id)] = true
	}
	for i := range p.Cues {
		if selected[p.Cues[i].ID] {
			p.Cues[i].StartMS += offsetMS
			p.Cues[i].EndMS += offsetMS
			p.Cues[i].CueRevision++
		}
	}
	sort.SliceStable(p.Cues, func(i, j int) bool { return p.Cues[i].StartMS < p.Cues[j].StartMS })
	for i := range p.Cues {
		p.Cues[i].Sequence = i + 1
	}
	p.Checks = []RuleCheck{}
	p.refreshEvidence()
	p.UpdatedAt = now.UTC()
	return preview, nil
}

func CheckRunDiff(runs []RuleCheckRun) RuleCheckDiff {
	result := RuleCheckDiff{NewFailures: []CheckResultRef{}, Fixed: []CheckResultRef{}, PersistentFailure: []CheckResultRef{}, UnchangedWarnings: []CheckResultRef{}, DeletedResults: []CheckResultRef{}}
	if len(runs) == 0 {
		return result
	}
	current := runs[len(runs)-1].Results
	previous := []RuleCheck{}
	if len(runs) > 1 {
		previous = runs[len(runs)-2].Results
	}
	key := func(c RuleCheck) string { return c.CueID + "\x00" + c.Rule }
	prev := map[string]RuleCheck{}
	curr := map[string]RuleCheck{}
	for _, c := range previous {
		prev[key(c)] = c
	}
	for _, c := range current {
		curr[key(c)] = c
	}
	ref := func(c RuleCheck) CheckResultRef {
		return CheckResultRef{CueID: c.CueID, Rule: c.Rule, Level: c.Level, Message: c.Message}
	}
	for k, c := range curr {
		old, exists := prev[k]
		if !c.Passed && c.Level == "error" {
			if exists && !old.Passed {
				result.PersistentFailure = append(result.PersistentFailure, ref(c))
			} else {
				result.NewFailures = append(result.NewFailures, ref(c))
			}
		} else if !c.Passed && c.Level == "warning" && exists && !old.Passed {
			result.UnchangedWarnings = append(result.UnchangedWarnings, ref(c))
		}
		if exists && !old.Passed && c.Passed {
			result.Fixed = append(result.Fixed, ref(old))
		}
	}
	for k, old := range prev {
		if _, exists := curr[k]; !exists {
			result.DeletedResults = append(result.DeletedResults, ref(old))
		}
	}
	return result
}

func (p *CaptionProject) ReleasePreview() ReleasePreview {
	preview := ReleasePreview{CurrentRevision: p.Revision, FrozenRevision: p.Revision + 1, CueCount: len(p.Cues), CaptionChecksum: p.CaptionChecksum(), MediaChecksum: p.MediaChecksum, Blockers: []ReleaseBlocker{}}
	if p.Status != StatusReady {
		preview.Blockers = append(preview.Blockers, ReleaseBlocker{Category: "status", Message: "项目尚未进入待发布状态"})
	}
	if len(p.CheckRuns) == 0 || p.CheckRuns[len(p.CheckRuns)-1].ProjectRevision != p.Revision {
		preview.Blockers = append(preview.Blockers, ReleaseBlocker{Category: "checks", Message: "最近规则检查未绑定当前项目修订"})
	} else {
		for _, check := range p.Checks {
			if !check.Passed && check.Level == "error" {
				preview.Blockers = append(preview.Blockers, ReleaseBlocker{ID: check.CueID, Category: check.Rule, Message: check.Message})
			}
		}
	}
	for _, f := range p.Findings {
		if f.Status != FindingResolved || !f.EvidenceValid {
			preview.Blockers = append(preview.Blockers, ReleaseBlocker{ID: f.ID, Category: f.Category, Message: "审校问题未解决或整改证据无效"})
		}
	}
	material := p.ID + "\x00" + strconv.FormatInt(p.Revision, 10) + "\x00" + preview.CaptionChecksum + "\x00" + p.MediaChecksum
	sum := sha256.Sum256([]byte(material))
	preview.ConfirmationToken = hex.EncodeToString(sum[:])
	return preview
}

func VerifyManifest(p *CaptionProject, approval *AuditEvent) ManifestIntegrity {
	report := ManifestIntegrity{Complete: true, Checks: []IntegrityItem{}}
	add := func(name string, passed bool, reason string, expected, actual any) {
		report.Checks = append(report.Checks, IntegrityItem{Name: name, Passed: passed, Reason: reason, Expected: expected, Actual: actual})
		if !passed {
			report.Complete = false
		}
	}
	if p.Manifest == nil {
		add("manifest", false, "发布清单不存在", "已发布清单", nil)
		return report
	}
	m := p.Manifest
	add("caption_checksum", m.CaptionChecksum == p.CaptionChecksum(), "冻结字幕内容校验", p.CaptionChecksum(), m.CaptionChecksum)
	add("cue_count", m.CueCount == len(p.Cues), "冻结字幕数量", len(p.Cues), m.CueCount)
	add("project_revision", m.ProjectRevision == p.Revision, "冻结项目修订", p.Revision, m.ProjectRevision)
	add("media_checksum", m.MediaChecksum == p.MediaChecksum, "素材基线校验值", p.MediaChecksum, m.MediaChecksum)
	add("manifest_version", m.ManifestVersion == "1", "清单版本", "1", m.ManifestVersion)
	if approval == nil {
		add("approval_audit", false, "批准审计事件不存在", "release.approved", nil)
	} else {
		actorOK := approval.Actor == m.ApprovedBy
		revisionOK := approval.Revision == m.ProjectRevision
		timeOK := approval.CreatedAt.Equal(m.ApprovedAt)
		add("approval_actor", actorOK, "批准人与审计事件交叉核对", m.ApprovedBy, approval.Actor)
		add("approval_revision", revisionOK, "批准修订与审计事件交叉核对", m.ProjectRevision, approval.Revision)
		add("approval_time", timeOK, "批准时间与审计事件交叉核对", m.ApprovedAt, approval.CreatedAt)
	}
	return report
}
