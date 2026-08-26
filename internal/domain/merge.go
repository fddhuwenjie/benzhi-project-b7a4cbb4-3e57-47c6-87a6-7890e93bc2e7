package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CueMergePreview 是相邻字幕合并的只读预览。
type CueMergePreview struct {
	ProjectID              string     `json:"project_id"`
	ProjectRevision        int64      `json:"project_revision"`
	PreviewRevision        int64      `json:"preview_revision"`
	ExpectedRevision       int64      `json:"expected_revision"`
	CueIDs                 []string   `json:"cue_ids"`
	FirstCueID             string     `json:"first_cue_id"`
	SecondCueID            string     `json:"second_cue_id"`
	MergedStartMS          int64      `json:"merged_start_ms"`
	MergedEndMS            int64      `json:"merged_end_ms"`
	MergedText             string     `json:"merged_text"`
	MergedSpeaker          string     `json:"merged_speaker"`
	SpeakerConflict        bool       `json:"speaker_conflict"`
	FirstSpeaker           string     `json:"first_speaker"`
	SecondSpeaker          string     `json:"second_speaker"`
	MergedSoundDescription string     `json:"merged_sound_description"`
	MergedCue              CaptionCue `json:"merged_cue"`
	ConfirmationToken      string     `json:"confirmation_token"`
}

type CueMergeResult struct {
	Preview      CueMergePreview `json:"preview"`
	Cue          CaptionCue      `json:"cue"`
	MergedCue    CaptionCue      `json:"merged_cue"`
	RemovedCueID string          `json:"removed_cue_id"`
}

func (p *CaptionProject) PreviewCueMerge(cueIDs []string, expectedRevision int64) (*CueMergePreview, error) {
	if p.Status == StatusReleased {
		return nil, Forbidden("已发布的字幕母版不可合并")
	}
	if p.Status != StatusDraft && p.Status != StatusChanges {
		return nil, Conflict("仅草稿或退回整改状态可合并字幕段")
	}
	if expectedRevision <= 0 {
		return nil, Invalid("expected_revision 必须大于零", "expected_revision")
	}
	if expectedRevision != p.Revision {
		return nil, ConflictWithDetails("合并预览修订已失效", map[string]any{"current_revision": p.Revision, "expected_revision": expectedRevision})
	}
	if len(cueIDs) != 2 || strings.TrimSpace(cueIDs[0]) == "" || strings.TrimSpace(cueIDs[1]) == "" || strings.TrimSpace(cueIDs[0]) == strings.TrimSpace(cueIDs[1]) {
		return nil, Invalid("必须选择两个不同的字幕段", "cue_ids")
	}
	firstID, secondID := strings.TrimSpace(cueIDs[0]), strings.TrimSpace(cueIDs[1])
	ordered := append([]CaptionCue(nil), p.Cues...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartMS < ordered[j].StartMS })
	firstIndex, secondIndex := -1, -1
	for i := range ordered {
		if ordered[i].ID == firstID {
			firstIndex = i
		}
		if ordered[i].ID == secondID {
			secondIndex = i
		}
	}
	if firstIndex < 0 {
		return nil, NotFound("字幕段", firstID)
	}
	if secondIndex < 0 {
		return nil, NotFound("字幕段", secondID)
	}
	if secondIndex != firstIndex+1 {
		return nil, Conflict("只能合并时间顺序相邻的两个字幕段")
	}
	first, second := ordered[firstIndex], ordered[secondIndex]
	if first.StartMS < 0 || second.EndMS > p.DurationMS || first.EndMS <= first.StartMS || second.EndMS <= second.StartMS {
		return nil, Invalid("待合并字幕段超出节目时间边界", "cue_ids")
	}
	if first.EndMS > second.StartMS {
		return nil, Conflict("待合并字幕段存在时间重叠")
	}
	for _, finding := range p.Findings {
		if finding.CueID == first.ID || finding.CueID == second.ID {
			return nil, ConflictWithDetails("待合并字幕段存在审校问题，无法合并", map[string]any{"finding_id": finding.ID, "cue_id": finding.CueID})
		}
	}
	text := strings.TrimSpace(first.Text)
	if secondText := strings.TrimSpace(second.Text); secondText != "" {
		if text != "" {
			text += "\n"
		}
		text += secondText
	}
	if text == "" {
		return nil, Invalid("合并后的字幕正文不能为空", "merged_text")
	}
	soundParts := []string{}
	if v := strings.TrimSpace(first.SoundDescription); v != "" {
		soundParts = append(soundParts, v)
	}
	if v := strings.TrimSpace(second.SoundDescription); v != "" {
		soundParts = append(soundParts, v)
	}
	speakerConflict := strings.TrimSpace(first.Speaker) != strings.TrimSpace(second.Speaker)
	mergedSpeaker := strings.TrimSpace(first.Speaker)
	if !speakerConflict {
		mergedSpeaker = strings.TrimSpace(second.Speaker)
	}
	tokenMaterial := fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%d\x00%d\x00%s", p.ID, p.Revision, first.ID, second.ID, first.StartMS, second.EndMS, text)
	sum := sha256.Sum256([]byte(tokenMaterial))
	mergedCue := CaptionCue{ID: first.ID, ProjectID: p.ID, Sequence: first.Sequence, StartMS: first.StartMS, EndMS: second.EndMS, Speaker: mergedSpeaker, Text: text, SoundDescription: strings.Join(soundParts, "\n"), CueRevision: first.CueRevision + 1}
	return &CueMergePreview{ProjectID: p.ID, ProjectRevision: p.Revision, PreviewRevision: p.Revision, ExpectedRevision: expectedRevision, CueIDs: []string{first.ID, second.ID}, FirstCueID: first.ID, SecondCueID: second.ID, MergedStartMS: first.StartMS, MergedEndMS: second.EndMS, MergedText: text, MergedSpeaker: mergedSpeaker, SpeakerConflict: speakerConflict, FirstSpeaker: strings.TrimSpace(first.Speaker), SecondSpeaker: strings.TrimSpace(second.Speaker), MergedSoundDescription: strings.Join(soundParts, "\n"), MergedCue: mergedCue, ConfirmationToken: hex.EncodeToString(sum[:])}, nil
}

func (p *CaptionProject) ApplyCueMerge(cueIDs []string, mergedSpeaker string, expectedRevision, previewRevision int64, confirmationToken string, now time.Time) (*CueMergeResult, error) {
	if previewRevision != p.Revision {
		return nil, ConflictWithDetails("合并预览修订已失效", map[string]any{"current_revision": p.Revision, "preview_revision": previewRevision})
	}
	preview, err := p.PreviewCueMerge(cueIDs, expectedRevision)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(confirmationToken) == "" || !strings.EqualFold(strings.TrimSpace(confirmationToken), preview.ConfirmationToken) {
		return nil, Conflict("合并确认令牌无效或预览内容已变化")
	}
	mergedSpeaker = strings.TrimSpace(mergedSpeaker)
	if preview.SpeakerConflict && mergedSpeaker == "" {
		return nil, Invalid("说话人冲突时必须提供合并说话人", "merged_speaker")
	}
	if mergedSpeaker == "" {
		mergedSpeaker = preview.MergedSpeaker
	}
	var merged CaptionCue
	updated := make([]CaptionCue, 0, len(p.Cues)-1)
	for _, cue := range p.Cues {
		switch cue.ID {
		case preview.FirstCueID:
			merged = cue
			merged.EndMS = preview.MergedEndMS
			merged.Text = preview.MergedText
			merged.Speaker = mergedSpeaker
			merged.SoundDescription = preview.MergedSoundDescription
			merged.CueRevision = cue.CueRevision + 1
			updated = append(updated, merged)
		case preview.SecondCueID:
			continue
		default:
			updated = append(updated, cue)
		}
	}
	sort.SliceStable(updated, func(i, j int) bool { return updated[i].StartMS < updated[j].StartMS })
	for i := range updated {
		updated[i].Sequence = i + 1
		updated[i].ProjectID = p.ID
	}
	p.Cues = updated
	p.Checks = []RuleCheck{}
	p.refreshEvidence()
	p.UpdatedAt = now.UTC()
	return &CueMergeResult{Preview: *preview, Cue: merged, MergedCue: merged, RemovedCueID: preview.SecondCueID}, nil
}
