package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"caption-release-workbench/internal/domain"
)

func (s *Service) FindingWorklist(ctx context.Context, projectID string, query domain.FindingWorklistQuery) (*domain.FindingWorklist, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, domain.Invalid("项目 ID 不能为空", "project_id")
	}
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return domain.QueryFindingWorklist(project, query)
}

func (s *Service) ReverificationEvidenceSummary(ctx context.Context, projectID string, expectedRevision int64) (*domain.ReverificationEvidenceSummary, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, domain.Invalid("项目 ID 不能为空", "project_id")
	}
	if expectedRevision <= 0 {
		return nil, domain.Invalid("expected_revision 必须大于零", "expected_revision")
	}
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.Status != domain.StatusChanges && project.Status != domain.StatusReverification && project.Status != domain.StatusReleased {
		return nil, domain.Conflict("仅整改、定向复验或已发布项目可查询复验证据汇总")
	}
	out := &domain.ReverificationEvidenceSummary{ProjectID: project.ID, ProjectRevision: project.Revision, ExpectedRevision: expectedRevision, RevisionMatches: project.Revision == expectedRevision, ReadOnly: project.Status == domain.StatusReleased, EligibleFindingIDs: []string{}, Items: []domain.ReverificationEvidenceItem{}}
	for _, finding := range project.Findings {
		item := domain.ReverificationEvidenceItem{FindingID: finding.ID, CueID: finding.CueID, ReportedCueRevision: finding.ReportedCueRevision, ResolvedCueRevision: finding.ResolvedCueRevision, ResolutionNote: finding.ResolutionNote, Changes: []domain.CueFieldChange{}, EvidenceStatus: "missing", BlockReason: "尚未记录整改快照"}
		item.ErrorCategory = "not_remediated"
		if cue := cueByID(project.Cues, finding.CueID); cue != nil {
			item.CueSequence, item.StartMS, item.EndMS = cue.Sequence, cue.StartMS, cue.EndMS
		}
		if finding.Status != domain.FindingRemediated {
			item.BlockReason = "问题尚未进入待复验状态"
		}
		if finding.ResolvedCueRevision > 0 {
			reported, rerr := s.checkedCueAtRevision(ctx, project, finding.CueID, finding.ReportedCueRevision)
			resolved, verr := s.checkedCueAtRevision(ctx, project, finding.CueID, finding.ResolvedCueRevision)
			if rerr == nil && verr == nil {
				var current *domain.CaptionCue
				if cue := cueByID(project.Cues, finding.CueID); cue != nil {
					copyCue := *cue
					current = &copyCue
				}
				evidence := domain.CompareFindingEvidence(project.ID, finding, reported, resolved, current)
				item.Changes = evidence.Changes
				switch evidence.Status {
				case "valid":
					item.EvidenceStatus = "valid"
					item.BlockReason = ""
					item.ErrorCategory = ""
				case "stale":
					item.EvidenceStatus = "stale"
					item.BlockReason = "整改后字幕已再次编辑"
					item.ErrorCategory = "stale_evidence"
				default:
					item.EvidenceStatus = "missing"
					item.BlockReason = "整改快照内容无效"
					item.ErrorCategory = "snapshot_invalid"
				}
			} else {
				item.EvidenceStatus = "missing"
				if isSnapshotMissing(rerr) || isSnapshotMissing(verr) {
					item.BlockReason = "历史字幕快照缺失"
					item.ErrorCategory = "snapshot_missing"
				} else {
					item.BlockReason = "历史字幕快照校验失败"
					item.ErrorCategory = "snapshot_invalid"
				}
			}
		}
		if finding.Status == domain.FindingRemediated && item.EvidenceStatus == "valid" && out.RevisionMatches && !out.ReadOnly {
			item.Eligible = true
			out.EligibleFindingIDs = append(out.EligibleFindingIDs, finding.ID)
		}
		switch item.EvidenceStatus {
		case "valid":
			out.ValidCount++
		case "stale":
			out.StaleCount++
		default:
			out.MissingCount++
		}
		out.Items = append(out.Items, item)
	}
	domain.SortEvidenceItems(out.Items)
	return out, nil
}

func isSnapshotMissing(err error) bool {
	if err == nil {
		return false
	}
	var business *domain.BusinessError
	return errors.As(err, &business) && business.Code == domain.CodeNotFound
}

func (s *Service) checkedCueAtRevision(ctx context.Context, project *domain.CaptionProject, cueID string, revision int64) (*domain.CaptionCue, error) {
	cues, checksum, err := s.repo.RevisionCues(ctx, project.ID, revision)
	if err != nil {
		return nil, err
	}
	snapshot := *project
	snapshot.Cues = cues
	if snapshot.CaptionChecksum() != checksum {
		return nil, fmt.Errorf("快照校验值不一致")
	}
	for i := range cues {
		if cues[i].ID == cueID {
			cue := cues[i]
			return &cue, nil
		}
	}
	return nil, domain.NotFound("字幕段历史快照", fmt.Sprintf("%s@%d", cueID, revision))
}

func cueByID(cues []domain.CaptionCue, id string) *domain.CaptionCue {
	for i := range cues {
		if cues[i].ID == id {
			c := cues[i]
			return &c
		}
	}
	return nil
}

func (s *Service) VerificationPackageSummary(ctx context.Context, projectID string) (*domain.VerificationPackageSummary, error) {
	pack, integrity, err := s.buildVerificationPackage(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return &domain.VerificationPackageSummary{ProjectID: pack.ProjectID, ProjectRevision: pack.ProjectRevision, ManifestID: pack.ManifestID, ManifestVersion: pack.ManifestVersion, FormatVersion: pack.FormatVersion, CueCount: pack.CueCount, CaptionChecksum: pack.CaptionChecksum, MediaChecksum: pack.MediaChecksum, Integrity: integrity, DownloadReady: integrity.Complete}, nil
}

func (s *Service) VerificationPackage(ctx context.Context, projectID string) (*domain.VerificationPackage, error) {
	pack, integrity, err := s.buildVerificationPackage(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !integrity.Complete {
		failed := []string{}
		for _, check := range integrity.Checks {
			if !check.Passed {
				failed = append(failed, check.Name)
			}
		}
		return nil, domain.ConflictWithDetails("冻结字幕核验包完整性校验失败", map[string]any{"failed_checks": failed})
	}
	return pack, nil
}

func (s *Service) buildVerificationPackage(ctx context.Context, projectID string) (*domain.VerificationPackage, domain.ManifestIntegrity, error) {
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, domain.ManifestIntegrity{}, err
	}
	if project.Status != domain.StatusReleased {
		return nil, domain.ManifestIntegrity{}, domain.Conflict("仅已发布项目可生成冻结字幕核验包")
	}
	manifest, err := s.repo.Manifest(ctx, projectID)
	if err != nil {
		return nil, domain.ManifestIntegrity{}, err
	}
	frozen, snapshotChecksum, err := s.repo.RevisionCues(ctx, projectID, manifest.ProjectRevision)
	if err != nil {
		return nil, domain.ManifestIntegrity{}, domain.ConflictWithDetails("冻结修订快照不存在", map[string]any{"project_revision": manifest.ProjectRevision})
	}
	events, err := s.repo.AuditQuery(ctx, projectID, domain.AuditQuery{EventType: "release.approved", Limit: 200})
	if err != nil {
		return nil, domain.ManifestIntegrity{}, err
	}
	var approval *domain.AuditEvent
	if len(events.Events) > 0 {
		candidate := events.Events[len(events.Events)-1]
		approval = &candidate
	}
	pack, integrity := domain.BuildVerificationPackage(project, manifest, frozen, snapshotChecksum, approval)
	return pack, integrity, nil
}

func (s *Service) SearchCues(ctx context.Context, projectID string, query domain.CueSearchQuery) (*domain.CueSearchResult, error) {
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return domain.SearchCues(project, query)
}

func (s *Service) ConvertCheckFailures(ctx context.Context, projectID string, cmd ConvertCheckFailuresCommand) (*domain.MutationResult, bool, error) {
	detail := map[string]any{"check_run_id": strings.TrimSpace(cmd.CheckRunID), "check_revision": cmd.ExpectedRevision, "added_count": len(cmd.Selections)}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "findings.converted_from_checks", detail, func(project *domain.CaptionProject) (any, error) {
		findings, err := project.ConvertCheckFailures(cmd.CheckRunID, cmd.Actor, cmd.Selections, func() string { return s.id("finding") }, s.now())
		if err != nil {
			return nil, err
		}
		ids := make([]string, len(findings))
		rules := make([]string, len(findings))
		for i := range findings {
			ids[i], rules[i] = findings[i].ID, findings[i].SourceRule
		}
		detail["finding_ids"], detail["source_rules"] = ids, rules
		return findings, nil
	})
}

func (s *Service) FindingEvidence(ctx context.Context, projectID, findingID string, expectedRevision int64) (*domain.FindingEvidence, error) {
	if strings.TrimSpace(findingID) == "" {
		return nil, domain.Invalid("问题 ID 不能为空", "finding_id")
	}
	if expectedRevision <= 0 {
		return nil, domain.Invalid("expected_revision 必须大于零", "expected_revision")
	}
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.Revision != expectedRevision {
		return nil, domain.Conflict(fmt.Sprintf("修订冲突：当前为 %d，提交为 %d", project.Revision, expectedRevision))
	}
	finding := findFinding(project.Findings, findingID)
	if finding == nil {
		return nil, domain.NotFound("审校问题", findingID)
	}
	cacheKey := findingEvidenceErrorKey(project, *finding)
	if err := s.cachedFindingEvidenceError(cacheKey); err != nil {
		return nil, err
	}
	view, err := s.findingEvidence(ctx, project, *finding)
	if err != nil {
		if cacheableFindingEvidenceError(err) {
			s.cacheFindingEvidenceError(cacheKey, err)
		}
		return nil, err
	}
	return view, nil
}

// cacheableFindingEvidenceError reports whether an error originates from a
// deterministic business-level condition (such as a missing snapshot or a
// finding that has not recorded a remediation revision). Transient storage
// failures are not cached so that a subsequent query for the same project,
// finding and revision can re-read both the reported and resolved snapshots
// once the underlying store has recovered.
func cacheableFindingEvidenceError(err error) bool {
	var business *domain.BusinessError
	return errors.As(err, &business)
}

func findingEvidenceErrorKey(project *domain.CaptionProject, finding domain.ReviewFinding) string {
	return fmt.Sprintf("%s:%s:%d:%d:%d", project.ID, finding.ID, project.Revision, finding.ReportedCueRevision, finding.ResolvedCueRevision)
}

func (s *Service) cachedFindingEvidenceError(key string) error {
	s.findingEvidenceMu.RLock()
	defer s.findingEvidenceMu.RUnlock()
	return s.findingEvidenceErrors[key]
}

func (s *Service) cacheFindingEvidenceError(key string, err error) {
	s.findingEvidenceMu.Lock()
	defer s.findingEvidenceMu.Unlock()
	s.findingEvidenceErrors[key] = err
}

func (s *Service) findingEvidence(ctx context.Context, project *domain.CaptionProject, finding domain.ReviewFinding) (*domain.FindingEvidence, error) {
	if finding.ResolvedCueRevision <= 0 {
		return nil, domain.ConflictWithDetails("问题尚未记录整改版本", map[string]any{"finding_id": finding.ID})
	}
	reported, err := s.repo.CueAtRevision(ctx, project.ID, finding.CueID, finding.ReportedCueRevision)
	if err != nil {
		return nil, err
	}
	resolved, err := s.repo.CueAtRevision(ctx, project.ID, finding.CueID, finding.ResolvedCueRevision)
	if err != nil {
		return nil, err
	}
	var current *domain.CaptionCue
	for i := range project.Cues {
		if project.Cues[i].ID == finding.CueID {
			copyCue := project.Cues[i]
			current = &copyCue
			break
		}
	}
	view := domain.CompareFindingEvidence(project.ID, finding, reported, resolved, current)
	return &view, nil
}

func findFinding(findings []domain.ReviewFinding, id string) *domain.ReviewFinding {
	for i := range findings {
		if findings[i].ID == id {
			copyFinding := findings[i]
			return &copyFinding
		}
	}
	return nil
}

func (s *Service) AuditEventDetail(ctx context.Context, projectID string, eventID int64, include string, revision int64) (*domain.AuditEventDetail, error) {
	if eventID <= 0 {
		return nil, domain.Invalid("event_id 必须大于零", "event_id")
	}
	if include != "" && include != "revision_diff" && include != "manifest" {
		return nil, domain.Invalid("不支持的关联详情", "include")
	}
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	event, err := s.repo.AuditEvent(ctx, projectID, eventID)
	if err != nil {
		return nil, err
	}
	if revision != 0 && revision != event.Revision {
		return nil, domain.Invalid("修订号与审计事件不匹配", "revision")
	}
	if event.Revision <= 0 || event.Revision > project.Revision {
		return nil, domain.Invalid("审计事件修订超出项目范围", "revision")
	}
	result := &domain.AuditEventDetail{Event: *event}
	if include == "revision_diff" {
		if event.Revision == 1 {
			return nil, domain.Invalid("初始修订没有前序差异", "include")
		}
		result.RevisionDiff, err = s.RevisionDiff(ctx, projectID, event.Revision-1, event.Revision)
		if err != nil {
			return nil, err
		}
	}
	if include == "manifest" {
		if event.Type != "release.approved" {
			return nil, domain.Invalid("该事件没有发布清单关联", "include")
		}
		result.ManifestReport, err = s.ManifestReport(ctx, projectID)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
