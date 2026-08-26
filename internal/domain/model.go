package domain

import "time"

type ProjectStatus string

const (
	StatusDraft          ProjectStatus = "draft"
	StatusInReview       ProjectStatus = "in_review"
	StatusChanges        ProjectStatus = "changes_requested"
	StatusReverification ProjectStatus = "reverification"
	StatusReady          ProjectStatus = "ready_for_release"
	StatusReleased       ProjectStatus = "released"
)

type CaptionProject struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	DurationMS    int64            `json:"duration_ms"`
	Language      string           `json:"language"`
	MediaChecksum string           `json:"media_checksum"`
	StyleProfile  string           `json:"style_profile"`
	Assignee      string           `json:"assignee"`
	Status        ProjectStatus    `json:"status"`
	Revision      int64            `json:"revision"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Cues          []CaptionCue     `json:"cues"`
	Checks        []RuleCheck      `json:"checks"`
	CheckRuns     []RuleCheckRun   `json:"check_runs"`
	Findings      []ReviewFinding  `json:"findings"`
	Manifest      *ReleaseManifest `json:"manifest,omitempty"`
}

type CaptionCue struct {
	ID               string `json:"id"`
	ProjectID        string `json:"project_id"`
	Sequence         int    `json:"sequence"`
	StartMS          int64  `json:"start_ms"`
	EndMS            int64  `json:"end_ms"`
	Speaker          string `json:"speaker"`
	Text             string `json:"text"`
	SoundDescription string `json:"sound_description"`
	CueRevision      int64  `json:"cue_revision"`
}

type RuleCheck struct {
	ID        string    `json:"id"`
	CueID     string    `json:"cue_id,omitempty"`
	Rule      string    `json:"rule"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Passed    bool      `json:"passed"`
	CheckedAt time.Time `json:"checked_at"`
}

// RuleCheckRun 是一次不可变的规则检查快照。字幕保存会清空当前 Checks，
// 因而只有仍被聚合引用的最后一次运行可用于后续流程。
type RuleCheckRun struct {
	ID              string      `json:"id"`
	ProjectRevision int64       `json:"project_revision"`
	RunAt           time.Time   `json:"run_at"`
	Results         []RuleCheck `json:"results"`
}

type CheckResultRef struct {
	CueID   string `json:"cue_id,omitempty"`
	Rule    string `json:"rule"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type RuleCheckDiff struct {
	NewFailures       []CheckResultRef `json:"new_failures"`
	Fixed             []CheckResultRef `json:"fixed"`
	PersistentFailure []CheckResultRef `json:"persistent_failures"`
	UnchangedWarnings []CheckResultRef `json:"unchanged_warnings"`
	DeletedResults    []CheckResultRef `json:"deleted_results"`
}

type FindingStatus string

const (
	FindingOpen       FindingStatus = "open"
	FindingRemediated FindingStatus = "remediated"
	FindingResolved   FindingStatus = "resolved"
	FindingRejected   FindingStatus = "rejected"
)

type ReviewFinding struct {
	ID                  string          `json:"id"`
	ProjectID           string          `json:"project_id"`
	CueID               string          `json:"cue_id"`
	Category            string          `json:"category"`
	Severity            string          `json:"severity"`
	Description         string          `json:"description"`
	Status              FindingStatus   `json:"status"`
	ReportedBy          string          `json:"reported_by"`
	ReportedCueRevision int64           `json:"reported_cue_revision"`
	ResolutionNote      string          `json:"resolution_note,omitempty"`
	ResolvedCueRevision int64           `json:"resolved_cue_revision,omitempty"`
	EvidenceValid       bool            `json:"evidence_valid"`
	VerifiedBy          string          `json:"verified_by,omitempty"`
	VerifiedAt          *time.Time      `json:"verified_at,omitempty"`
	ReviewHistory       []FindingReview `json:"review_history"`
	SourceCheckRunID    string          `json:"source_check_run_id,omitempty"`
	SourceRule          string          `json:"source_rule,omitempty"`
	SourceCheckRevision int64           `json:"source_check_revision,omitempty"`
}

type FindingReview struct {
	Reviewer    string    `json:"reviewer"`
	Resolved    bool      `json:"resolved"`
	CueRevision int64     `json:"cue_revision"`
	ReviewedAt  time.Time `json:"reviewed_at"`
}

type ReleaseManifest struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	ProjectRevision int64     `json:"project_revision"`
	CueCount        int       `json:"cue_count"`
	CaptionChecksum string    `json:"caption_checksum"`
	MediaChecksum   string    `json:"media_checksum"`
	ApprovedBy      string    `json:"approved_by"`
	ApprovedAt      time.Time `json:"approved_at"`
	ManifestVersion string    `json:"manifest_version"`
}

type ReleaseBlocker struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

type ReleasePreview struct {
	CurrentRevision   int64            `json:"current_revision"`
	FrozenRevision    int64            `json:"frozen_revision"`
	CueCount          int              `json:"cue_count"`
	CaptionChecksum   string           `json:"caption_checksum"`
	MediaChecksum     string           `json:"media_checksum"`
	ConfirmationToken string           `json:"confirmation_token"`
	Blockers          []ReleaseBlocker `json:"blockers"`
}

type IntegrityItem struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Reason   string `json:"reason,omitempty"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
}

type ManifestIntegrity struct {
	Complete bool            `json:"complete"`
	Checks   []IntegrityItem `json:"checks"`
}

type ManifestReport struct {
	Manifest  *ReleaseManifest  `json:"manifest"`
	Integrity ManifestIntegrity `json:"integrity"`
}

type CueShiftChange struct {
	CueID      string `json:"cue_id"`
	OldStartMS int64  `json:"old_start_ms"`
	OldEndMS   int64  `json:"old_end_ms"`
	NewStartMS int64  `json:"new_start_ms"`
	NewEndMS   int64  `json:"new_end_ms"`
}

type CueShiftPreview struct {
	ProjectRevision int64            `json:"project_revision"`
	OffsetMS        int64            `json:"offset_ms"`
	Changes         []CueShiftChange `json:"changes"`
}

type AuditEvent struct {
	ID        int64          `json:"id"`
	ProjectID string         `json:"project_id"`
	Type      string         `json:"type"`
	Actor     string         `json:"actor"`
	Revision  int64          `json:"revision"`
	Detail    map[string]any `json:"detail,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type RiskReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type ProjectRisk struct {
	Level              RiskLevel    `json:"level"`
	Score              int          `json:"score"`
	FailedRuleCount    int          `json:"failed_rule_count"`
	SevereFindingCount int          `json:"severe_finding_count"`
	OpenFindingCount   int          `json:"open_finding_count"`
	StaleDays          int          `json:"stale_days"`
	Reasons            []RiskReason `json:"reasons"`
}

type ProjectSummary struct {
	ID                 string        `json:"id"`
	Title              string        `json:"title"`
	Language           string        `json:"language"`
	Assignee           string        `json:"assignee"`
	Status             ProjectStatus `json:"status"`
	Revision           int64         `json:"revision"`
	UpdatedAt          time.Time     `json:"updated_at"`
	FailedRuleCount    int           `json:"failed_rule_count"`
	OpenFindingCount   int           `json:"open_finding_count"`
	SevereFindingCount int           `json:"severe_finding_count"`
	Risk               ProjectRisk   `json:"risk"`
}

type QueueFilter struct {
	Status                 ProjectStatus
	Language, Assignee     string
	Risk                   RiskLevel
	Sort                   string
	UpdatedFrom, UpdatedTo *time.Time
}
type QueueStats struct {
	StatusCounts       map[ProjectStatus]int `json:"status_counts"`
	RiskCounts         map[RiskLevel]int     `json:"risk_counts"`
	FailedRuleCount    int                   `json:"failed_rule_count"`
	OpenFindingCount   int                   `json:"open_finding_count"`
	SevereFindingCount int                   `json:"severe_finding_count"`
	LatestUpdatedAt    *time.Time            `json:"latest_updated_at,omitempty"`
}
type ProjectQueue struct {
	Projects []ProjectSummary `json:"projects"`
	Stats    QueueStats       `json:"stats"`
}
type CueFieldChange struct {
	Field    string `json:"field"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}
type CueRevisionDiff struct {
	CueID      string           `json:"cue_id"`
	ChangeType string           `json:"change_type"`
	Changes    []CueFieldChange `json:"changes,omitempty"`
}
type RevisionDiff struct {
	ProjectID    string            `json:"project_id"`
	FromRevision int64             `json:"from_revision"`
	ToRevision   int64             `json:"to_revision"`
	Changes      []CueRevisionDiff `json:"changes"`
	FromChecksum string            `json:"from_checksum"`
	ToChecksum   string            `json:"to_checksum"`
}
type CheckHistoryFilter struct {
	RunID, Rule, CueID, Level string
	From, To                  *time.Time
}
type CheckHistoryRun struct {
	ID              string      `json:"id"`
	ProjectRevision int64       `json:"project_revision"`
	RunAt           time.Time   `json:"run_at"`
	Results         []RuleCheck `json:"results"`
	Current         bool        `json:"current"`
}
type CheckHistorySummary struct {
	Runs               []CheckHistoryRun `json:"runs"`
	RuleCounts         map[string]int    `json:"rule_counts"`
	RunCount           int               `json:"run_count"`
	NewFailures        int               `json:"new_failures"`
	PersistentFailures int               `json:"persistent_failures"`
	Fixed              int               `json:"fixed"`
	Warnings           int               `json:"warnings"`
}
type AuditQuery struct {
	Actor, EventType string
	From, To         *time.Time
	After            int64
	Limit            int
}
type AuditSummary struct {
	ByEventType map[string]int `json:"by_event_type"`
	ByActor     map[string]int `json:"by_actor"`
	NextAfter   int64          `json:"next_after"`
}
type AuditPage struct {
	Events  []AuditEvent `json:"events"`
	Summary AuditSummary `json:"summary"`
}

type CueSearchQuery struct {
	Keyword          string
	StartMS, EndMS   *int64
	ExpectedRevision int64
}

type CueSearchHit struct {
	Cue           CaptionCue  `json:"cue"`
	MatchedFields []string    `json:"matched_fields"`
	Previous      *CaptionCue `json:"previous,omitempty"`
	Next          *CaptionCue `json:"next,omitempty"`
}

type CueSearchResult struct {
	ProjectID       string         `json:"project_id"`
	ProjectRevision int64          `json:"project_revision"`
	RevisionMatches bool           `json:"revision_matches"`
	ReadOnly        bool           `json:"read_only"`
	Hits            []CueSearchHit `json:"hits"`
}

type CheckFindingSelection struct {
	CheckID string `json:"check_id"`
	CueID   string `json:"cue_id"`
}

type FindingEvidence struct {
	ProjectID           string           `json:"project_id"`
	FindingID           string           `json:"finding_id"`
	CueID               string           `json:"cue_id"`
	ReportedCueRevision int64            `json:"reported_cue_revision"`
	ResolvedCueRevision int64            `json:"resolved_cue_revision"`
	CurrentCueRevision  int64            `json:"current_cue_revision"`
	ResolutionNote      string           `json:"resolution_note"`
	Valid               bool             `json:"valid"`
	Status              string           `json:"status"`
	Changes             []CueFieldChange `json:"changes"`
}

type AuditEventDetail struct {
	Event          AuditEvent      `json:"event"`
	RevisionDiff   *RevisionDiff   `json:"revision_diff,omitempty"`
	ManifestReport *ManifestReport `json:"manifest_report,omitempty"`
}
