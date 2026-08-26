package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type AccessibilityPolicy struct {
	MaxCharactersPerSecond float64
	MinimumGapMS           int64
	RequireSpeaker         bool
	RequireBracketedSound  bool
}

func DefaultAccessibilityPolicy() AccessibilityPolicy {
	return AccessibilityPolicy{MaxCharactersPerSecond: 20, MinimumGapMS: 80, RequireSpeaker: true, RequireBracketedSound: true}
}

func EvaluateAccessibility(cues []CaptionCue, policy AccessibilityPolicy, checkedAt time.Time) []RuleCheck {
	checks := make([]RuleCheck, 0, len(cues)*4+1)
	add := func(cueID, rule, level, message string, passed bool) {
		checks = append(checks, RuleCheck{ID: fmt.Sprintf("check-%d", len(checks)+1), CueID: cueID, Rule: rule, Level: level, Message: message, Passed: passed, CheckedAt: checkedAt.UTC()})
	}
	if len(cues) == 0 {
		add("", "timeline.required", "error", "字幕时间轴不能为空", false)
		return checks
	}
	ordered := append([]CaptionCue(nil), cues...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	for i, cue := range ordered {
		durationSeconds := float64(cue.EndMS-cue.StartMS) / 1000
		charactersPerSecond := float64(utf8.RuneCountInString(cue.Text)) / durationSeconds
		readingPassed := durationSeconds > 0 && charactersPerSecond <= policy.MaxCharactersPerSecond
		add(cue.ID, "reading_speed", "error", fmt.Sprintf("阅读速度 %.1f 字/秒，上限 %.1f", charactersPerSecond, policy.MaxCharactersPerSecond), readingPassed)
		speakerPassed := !policy.RequireSpeaker || cue.Text == "" || strings.TrimSpace(cue.Speaker) != ""
		add(cue.ID, "speaker", "error", "对白字幕必须标注说话人", speakerPassed)
		sound := strings.TrimSpace(cue.SoundDescription)
		soundPassed := !policy.RequireBracketedSound || sound == "" || (strings.HasPrefix(sound, "[") && strings.HasSuffix(sound, "]"))
		add(cue.ID, "sound_description", "error", "声音说明应使用方括号", soundPassed)
		if i > 0 {
			gap := cue.StartMS - ordered[i-1].EndMS
			add(cue.ID, "caption_gap", "warning", fmt.Sprintf("相邻字幕间隔为 %dms，建议不少于 %dms", gap, policy.MinimumGapMS), gap >= policy.MinimumGapMS)
		}
	}
	return checks
}

func ValidateRestoredProject(p *CaptionProject) error {
	if p == nil {
		return fmt.Errorf("项目聚合为空")
	}
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Title) == "" || p.DurationMS <= 0 || p.Revision <= 0 {
		return fmt.Errorf("项目基线或修订号无效")
	}
	if !validProjectStatus(p.Status) {
		return fmt.Errorf("未知项目状态 %q", p.Status)
	}
	cueIDs := make(map[string]CaptionCue, len(p.Cues))
	previousEnd := int64(-1)
	for index, cue := range p.Cues {
		if cue.ID == "" || cue.ProjectID != p.ID {
			return fmt.Errorf("字幕段 %d 的标识或项目引用无效", index+1)
		}
		if _, exists := cueIDs[cue.ID]; exists {
			return fmt.Errorf("字幕段 ID %q 重复", cue.ID)
		}
		if cue.Sequence != index+1 {
			return fmt.Errorf("字幕段 %q 序号不连续", cue.ID)
		}
		if cue.StartMS < 0 || cue.EndMS <= cue.StartMS || cue.EndMS > p.DurationMS {
			return fmt.Errorf("字幕段 %q 时间边界无效", cue.ID)
		}
		if previousEnd > cue.StartMS {
			return fmt.Errorf("字幕段 %q 与前一段重叠", cue.ID)
		}
		if strings.TrimSpace(cue.Text) == "" && strings.TrimSpace(cue.SoundDescription) == "" {
			return fmt.Errorf("字幕段 %q 内容为空", cue.ID)
		}
		cueIDs[cue.ID], previousEnd = cue, cue.EndMS
	}
	checkIDs := make(map[string]bool, len(p.Checks))
	for _, check := range p.Checks {
		if check.ID == "" || check.Rule == "" || (check.Level != "error" && check.Level != "warning") {
			return fmt.Errorf("规则检查记录无效")
		}
		if checkIDs[check.ID] {
			return fmt.Errorf("规则检查 ID %q 重复", check.ID)
		}
		if check.CueID != "" {
			if _, exists := cueIDs[check.CueID]; !exists {
				return fmt.Errorf("规则检查引用不存在的字幕段 %q", check.CueID)
			}
		}
		checkIDs[check.ID] = true
	}
	runIDs := make(map[string]bool, len(p.CheckRuns))
	for _, run := range p.CheckRuns {
		if run.ID == "" || run.ProjectRevision <= 0 || run.RunAt.IsZero() || runIDs[run.ID] {
			return fmt.Errorf("规则检查运行记录无效")
		}
		runIDs[run.ID] = true
	}
	findingIDs := make(map[string]bool, len(p.Findings))
	for _, finding := range p.Findings {
		if finding.ID == "" || finding.ProjectID != p.ID {
			return fmt.Errorf("审校问题标识或项目引用无效")
		}
		if findingIDs[finding.ID] {
			return fmt.Errorf("审校问题 ID %q 重复", finding.ID)
		}
		if _, exists := cueIDs[finding.CueID]; !exists {
			return fmt.Errorf("审校问题引用不存在的字幕段 %q", finding.CueID)
		}
		if !validFindingStatus(finding.Status) {
			return fmt.Errorf("审校问题 %q 状态无效", finding.ID)
		}
		if finding.ResolvedCueRevision < 0 {
			return fmt.Errorf("审校问题 %q 字幕修订无效", finding.ID)
		}
		if finding.ReportedCueRevision <= 0 {
			return fmt.Errorf("审校问题 %q 登记字幕版本无效", finding.ID)
		}
		cue := cueIDs[finding.CueID]
		expectedEvidence := finding.ResolvedCueRevision > 0 && cue.CueRevision == finding.ResolvedCueRevision && finding.Status != FindingRejected
		if finding.EvidenceValid != expectedEvidence {
			return fmt.Errorf("审校问题 %q 整改证据状态无效", finding.ID)
		}
		findingIDs[finding.ID] = true
	}
	if p.Status == StatusReleased {
		if p.Manifest == nil {
			return fmt.Errorf("已发布项目缺少发布清单")
		}
		if p.Manifest.ProjectID != p.ID {
			return fmt.Errorf("发布清单引用的项目无效")
		}
	} else if p.Manifest != nil {
		return fmt.Errorf("未发布项目不应包含发布清单")
	}
	return nil
}

func validProjectStatus(status ProjectStatus) bool {
	switch status {
	case StatusDraft, StatusInReview, StatusChanges, StatusReverification, StatusReady, StatusReleased:
		return true
	default:
		return false
	}
}

func ValidProjectStatus(status ProjectStatus) bool { return validProjectStatus(status) }

func validFindingStatus(status FindingStatus) bool {
	switch status {
	case FindingOpen, FindingRemediated, FindingResolved, FindingRejected:
		return true
	default:
		return false
	}
}
