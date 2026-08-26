package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RemediationItem struct {
	FindingID      string `json:"finding_id"`
	ResolutionNote string `json:"resolution_note"`
	CueRevision    int64  `json:"cue_revision"`
}

func (p *CaptionProject) RemediateBatch(items []RemediationItem, now time.Time) error {
	if p.Status != StatusChanges {
		return Conflict("仅整改状态可批量记录修订说明")
	}
	if len(items) == 0 {
		return Invalid("至少需要一项整改", "items")
	}
	seen := map[string]bool{}
	for i, it := range items {
		if seen[it.FindingID] {
			return Conflict("批次内问题重复")
		}
		seen[it.FindingID] = true
		f := p.findFinding(strings.TrimSpace(it.FindingID))
		if f == nil {
			return NotFound("审校问题", it.FindingID)
		}
		note := strings.TrimSpace(it.ResolutionNote)
		if note == "" {
			return Invalid(fmt.Sprintf("第 %d 项整改说明不能为空", i+1), "items")
		}
		if f.Status != FindingOpen && f.Status != FindingRejected && !(f.Status == FindingResolved && !f.EvidenceValid) {
			return Conflict(fmt.Sprintf("问题 %s 当前不可整改", f.ID))
		}
		cue := p.findCue(f.CueID)
		if cue == nil {
			return NotFound("字幕段", f.CueID)
		}
		if it.CueRevision <= 0 || cue.CueRevision != it.CueRevision {
			return Conflict(fmt.Sprintf("问题 %s 的字幕修订不匹配", f.ID))
		}
		min := f.ReportedCueRevision
		if f.ResolvedCueRevision > min {
			min = f.ResolvedCueRevision
		}
		if cue.CueRevision <= min {
			return Invalid(fmt.Sprintf("问题 %s 未形成新字幕版本", f.ID), "items")
		}
	}
	for _, it := range items {
		f := p.findFinding(it.FindingID)
		f.Status = FindingRemediated
		f.ResolutionNote = strings.TrimSpace(it.ResolutionNote)
		f.ResolvedCueRevision = it.CueRevision
		f.EvidenceValid = true
	}
	p.UpdatedAt = now.UTC()
	return nil
}

type VerificationItem struct {
	FindingID   string `json:"finding_id"`
	Resolved    *bool  `json:"resolved"`
	CueRevision int64  `json:"cue_revision"`
}

func (p *CaptionProject) VerifyBatch(items []VerificationItem, reviewer string, now time.Time) error {
	if p.Status != StatusReverification {
		return Conflict("仅定向复验状态可批量确认问题")
	}
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return Invalid("复验人不能为空", "actor")
	}
	if len(items) == 0 {
		return Invalid("至少需要一项复验结论", "items")
	}
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.FindingID] {
			return Conflict("批次内问题重复")
		}
		seen[it.FindingID] = true
		if it.Resolved == nil {
			return Invalid("每项必须给出解决或驳回结论", "items")
		}
		f := p.findFinding(it.FindingID)
		if f == nil {
			return NotFound("审校问题", it.FindingID)
		}
		if f.Status != FindingRemediated || !f.EvidenceValid || f.ResolvedCueRevision != it.CueRevision {
			return Conflict(fmt.Sprintf("问题 %s 的整改证据已失效", f.ID))
		}
	}
	t := now.UTC()
	for _, it := range items {
		f := p.findFinding(it.FindingID)
		f.VerifiedBy = reviewer
		f.VerifiedAt = &t
		f.ReviewHistory = append(f.ReviewHistory, FindingReview{Reviewer: reviewer, Resolved: *it.Resolved, CueRevision: f.ResolvedCueRevision, ReviewedAt: t})
		if *it.Resolved {
			f.Status = FindingResolved
		} else {
			f.Status = FindingRejected
			f.EvidenceValid = false
			p.Status = StatusChanges
		}
	}
	p.UpdatedAt = t
	return nil
}

func CompareCueSnapshots(projectID string, fromRev, toRev int64, from, to []CaptionCue, fromChecksum, toChecksum string) RevisionDiff {
	fm := map[string]CaptionCue{}
	tm := map[string]CaptionCue{}
	for _, c := range from {
		fm[c.ID] = c
	}
	for _, c := range to {
		tm[c.ID] = c
	}
	ids := map[string]bool{}
	for id := range fm {
		ids[id] = true
	}
	for id := range tm {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	out := RevisionDiff{ProjectID: projectID, FromRevision: fromRev, ToRevision: toRev, FromChecksum: fromChecksum, ToChecksum: toChecksum, Changes: []CueRevisionDiff{}}
	for _, id := range sorted {
		a, ao := fm[id]
		b, bo := tm[id]
		if !ao {
			out.Changes = append(out.Changes, CueRevisionDiff{CueID: id, ChangeType: "added"})
			continue
		}
		if !bo {
			out.Changes = append(out.Changes, CueRevisionDiff{CueID: id, ChangeType: "deleted"})
			continue
		}
		changes := []CueFieldChange{}
		add := func(name, oldv, newv string) {
			if oldv != newv {
				changes = append(changes, CueFieldChange{Field: name, OldValue: oldv, NewValue: newv})
			}
		}
		add("start_ms", strconv.FormatInt(a.StartMS, 10), strconv.FormatInt(b.StartMS, 10))
		add("end_ms", strconv.FormatInt(a.EndMS, 10), strconv.FormatInt(b.EndMS, 10))
		add("sequence", strconv.Itoa(a.Sequence), strconv.Itoa(b.Sequence))
		add("speaker", a.Speaker, b.Speaker)
		add("text", a.Text, b.Text)
		add("sound_description", a.SoundDescription, b.SoundDescription)
		if len(changes) > 0 {
			out.Changes = append(out.Changes, CueRevisionDiff{CueID: id, ChangeType: "modified", Changes: changes})
		}
	}
	return out
}
