package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type CaptionRollbackPreview struct {
	ProjectID         string            `json:"project_id"`
	CurrentRevision   int64             `json:"current_revision"`
	TargetRevision    int64             `json:"target_revision"`
	CurrentChecksum   string            `json:"current_checksum"`
	TargetChecksum    string            `json:"target_checksum"`
	Changes           []CueRevisionDiff `json:"changes"`
	ConfirmationToken string            `json:"confirmation_token"`
}

// BuildRollbackPreview validates a historical snapshot and creates a token
// bound to both revisions and checksums. The token is safe to recompute at
// confirmation time and therefore does not need a second persistence table.
func (p *CaptionProject) BuildRollbackPreview(targetRevision, expectedRevision int64, target []CaptionCue, targetChecksum string) (*CaptionRollbackPreview, error) {
	if p.Status == StatusReleased {
		return nil, Forbidden("已发布项目不可回滚字幕修订")
	}
	if p.Status != StatusDraft && p.Status != StatusChanges {
		return nil, Conflict("仅草稿或退回整改状态可回滚字幕修订")
	}
	if expectedRevision <= 0 || expectedRevision != p.Revision {
		return nil, ConflictWithDetails("回滚预览修订已失效", map[string]any{"current_revision": p.Revision, "expected_revision": expectedRevision})
	}
	if targetRevision <= 0 || targetRevision >= p.Revision {
		return nil, Invalid("目标修订必须早于当前修订", "target_revision")
	}
	if err := validateFrozenSnapshot(p.ID, target, p.DurationMS); err != nil {
		return nil, ConflictWithDetails("历史字幕快照校验失败", map[string]any{"target_revision": targetRevision, "error": err.Error()})
	}
	copyTarget := append([]CaptionCue(nil), target...)
	targetProject := *p
	targetProject.Cues = copyTarget
	computed := targetProject.CaptionChecksum()
	if strings.TrimSpace(targetChecksum) == "" || computed != targetChecksum {
		return nil, ConflictWithDetails("历史字幕快照校验值不一致", map[string]any{"target_revision": targetRevision})
	}
	currentChecksum := p.CaptionChecksum()
	tokenMaterial := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s", p.ID, p.Revision, targetRevision, currentChecksum, targetChecksum)
	sum := sha256.Sum256([]byte(tokenMaterial))
	return &CaptionRollbackPreview{ProjectID: p.ID, CurrentRevision: p.Revision, TargetRevision: targetRevision, CurrentChecksum: currentChecksum, TargetChecksum: targetChecksum, Changes: CompareCueSnapshots(p.ID, p.Revision, targetRevision, p.Cues, target, currentChecksum, targetChecksum).Changes, ConfirmationToken: hex.EncodeToString(sum[:])}, nil
}

func (p *CaptionProject) ApplyRollback(targetRevision, expectedRevision int64, target []CaptionCue, targetChecksum, confirmationToken string, now time.Time) error {
	preview, err := p.BuildRollbackPreview(targetRevision, expectedRevision, target, targetChecksum)
	if err != nil {
		return err
	}
	if strings.TrimSpace(confirmationToken) == "" || confirmationToken != preview.ConfirmationToken {
		return Conflict("回滚确认令牌无效或已过期")
	}
	targetIDs := make(map[string]bool, len(target))
	for _, cue := range target {
		targetIDs[cue.ID] = true
	}
	for _, finding := range p.Findings {
		if !targetIDs[finding.CueID] {
			return ConflictWithDetails("目标字幕快照缺少现有审校问题引用的字幕段", map[string]any{"finding_id": finding.ID, "cue_id": finding.CueID})
		}
	}
	p.Cues = append([]CaptionCue(nil), target...)
	p.Checks = []RuleCheck{}
	// Keep finding history, but every prior remediation must be re-established
	// against the restored timeline before it can be verified again.
	for i := range p.Findings {
		p.Findings[i].EvidenceValid = false
	}
	p.UpdatedAt = now.UTC()
	return nil
}

type ReverificationEvidenceItem struct {
	FindingID           string           `json:"finding_id"`
	CueID               string           `json:"cue_id"`
	CueSequence         int              `json:"cue_sequence"`
	StartMS             int64            `json:"start_ms"`
	EndMS               int64            `json:"end_ms"`
	ReportedCueRevision int64            `json:"reported_cue_revision"`
	ResolvedCueRevision int64            `json:"resolved_cue_revision"`
	ResolutionNote      string           `json:"resolution_note"`
	Changes             []CueFieldChange `json:"changes"`
	EvidenceStatus      string           `json:"evidence_status"`
	ErrorCategory       string           `json:"error_category,omitempty"`
	BlockReason         string           `json:"block_reason,omitempty"`
	Eligible            bool             `json:"eligible"`
}

type ReverificationEvidenceSummary struct {
	ProjectID          string                       `json:"project_id"`
	ProjectRevision    int64                        `json:"project_revision"`
	ExpectedRevision   int64                        `json:"expected_revision"`
	RevisionMatches    bool                         `json:"revision_matches"`
	ReadOnly           bool                         `json:"read_only"`
	EligibleFindingIDs []string                     `json:"eligible_finding_ids"`
	ValidCount         int                          `json:"valid_count"`
	MissingCount       int                          `json:"missing_count"`
	StaleCount         int                          `json:"stale_count"`
	Items              []ReverificationEvidenceItem `json:"items"`
}

func SortEvidenceItems(items []ReverificationEvidenceItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StartMS != items[j].StartMS {
			return items[i].StartMS < items[j].StartMS
		}
		if items[i].CueSequence != items[j].CueSequence {
			return items[i].CueSequence < items[j].CueSequence
		}
		return items[i].FindingID < items[j].FindingID
	})
}
