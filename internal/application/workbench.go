package application

import (
	"context"

	"caption-release-workbench/internal/domain"
)

type QualitySummary struct {
	CueCount             int     `json:"cue_count"`
	TimelineCoverage     float64 `json:"timeline_coverage"`
	FailedRuleCount      int     `json:"failed_rule_count"`
	WarningCount         int     `json:"warning_count"`
	OpenFindingCount     int     `json:"open_finding_count"`
	RemediatedCount      int     `json:"remediated_count"`
	ResolvedFindingCount int     `json:"resolved_finding_count"`
	RulesPassed          bool    `json:"rules_passed"`
	ReadyForRelease      bool    `json:"ready_for_release"`
}

type WorkbenchView struct {
	Project        *domain.CaptionProject `json:"project"`
	Quality        QualitySummary         `json:"quality"`
	AllowedActions []string               `json:"allowed_actions"`
	Audit          []domain.AuditEvent    `json:"audit"`
	NextAuditAfter int64                  `json:"next_audit_after"`
	CheckDiff      domain.RuleCheckDiff   `json:"check_diff"`
	ChecksCurrent  bool                   `json:"checks_current"`
	ReleasePreview *domain.ReleasePreview `json:"release_preview,omitempty"`
	ManifestReport *domain.ManifestReport `json:"manifest_report,omitempty"`
}

func (s *Service) GetWorkbench(ctx context.Context, projectID string) (*WorkbenchView, error) {
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	audit, err := s.repo.Audit(ctx, projectID, 0, 100)
	if err != nil {
		return nil, err
	}
	next := int64(0)
	if len(audit) > 0 {
		next = audit[len(audit)-1].ID
	}
	view := &WorkbenchView{
		Project: project, Quality: summarizeQuality(project),
		AllowedActions: allowedActions(project), Audit: audit, NextAuditAfter: next,
		CheckDiff: domain.CheckRunDiff(project.CheckRuns), ChecksCurrent: project.HasCurrentChecks(),
	}
	if project.Status == domain.StatusReady {
		preview := project.ReleasePreview()
		view.ReleasePreview = &preview
	}
	if project.Status == domain.StatusReleased {
		if err := s.populateReleasedManifest(ctx, projectID, view); err != nil {
			return nil, err
		}
	}
	return view, nil
}

func (s *Service) populateReleasedManifest(ctx context.Context, projectID string, view *WorkbenchView) error {
	manifest, err := s.ManifestReport(ctx, projectID)
	if err != nil {
		return err
	}
	view.ManifestReport = manifest
	return nil
}

func summarizeQuality(project *domain.CaptionProject) QualitySummary {
	summary := QualitySummary{CueCount: len(project.Cues), RulesPassed: project.CurrentChecksPassed()}
	coveredMS := int64(0)
	for _, cue := range project.Cues {
		coveredMS += cue.EndMS - cue.StartMS
	}
	if project.DurationMS > 0 {
		summary.TimelineCoverage = float64(coveredMS) / float64(project.DurationMS)
	}
	for _, check := range project.Checks {
		if check.Passed {
			continue
		}
		if check.Level == "warning" {
			summary.WarningCount++
		} else {
			summary.FailedRuleCount++
		}
	}
	for _, finding := range project.Findings {
		switch finding.Status {
		case domain.FindingResolved:
			summary.ResolvedFindingCount++
		case domain.FindingRemediated:
			summary.RemediatedCount++
		default:
			summary.OpenFindingCount++
		}
	}
	summary.ReadyForRelease = project.Status == domain.StatusReady && summary.RulesPassed && summary.OpenFindingCount == 0 && summary.RemediatedCount == 0
	return summary
}

func allowedActions(project *domain.CaptionProject) []string {
	actions := []string{"view", "view_audit"}
	switch project.Status {
	case domain.StatusDraft:
		actions = append(actions, "edit_cues", "shift_cues", "split_cue", "merge_cues", "run_checks")
		if project.HasCurrentChecks() {
			actions = append(actions, "submit_review")
		}
	case domain.StatusInReview:
		actions = append(actions, "add_finding", "review_decision")
	case domain.StatusChanges:
		actions = append(actions, "edit_cues", "shift_cues", "split_cue", "merge_cues", "rollback_cues", "run_checks", "remediate_findings", "reverification_evidence_summary")
		if project.CurrentChecksPassed() && allFindingsRemediated(project.Findings) {
			actions = append(actions, "submit_reverification")
		}
	case domain.StatusReverification:
		actions = append(actions, "verify_findings", "reverification_evidence_summary")
		if allFindingsResolved(project.Findings) {
			actions = append(actions, "complete_reverification")
		}
	case domain.StatusReady:
		actions = append(actions, "run_checks", "approve_release")
	case domain.StatusReleased:
		actions = append(actions, "view_manifest", "view_verification_package", "download_verification_package", "reverification_evidence_summary")
	}
	return actions
}

func allFindingsRemediated(findings []domain.ReviewFinding) bool {
	if len(findings) == 0 {
		return false
	}
	for _, finding := range findings {
		if finding.Status != domain.FindingRemediated && finding.Status != domain.FindingResolved {
			return false
		}
		if !finding.EvidenceValid {
			return false
		}
	}
	return true
}

func allFindingsResolved(findings []domain.ReviewFinding) bool {
	for _, finding := range findings {
		if finding.Status != domain.FindingResolved {
			return false
		}
	}
	return true
}
