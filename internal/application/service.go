package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"caption-release-workbench/internal/domain"
)

type Clock func() time.Time
type IDGenerator func(string) string

type Service struct {
	repo           domain.Repository
	now            Clock
	id             IDGenerator
	workbenchMu    sync.RWMutex
	workbenchCache map[string][]byte
}

func New(repo domain.Repository) *Service {
	return &Service{repo: repo, now: time.Now, id: randomID, workbenchCache: map[string][]byte{}}
}

func NewWithDependencies(repo domain.Repository, now Clock, id IDGenerator) *Service {
	return &Service{repo: repo, now: now, id: id, workbenchCache: map[string][]byte{}}
}

type WriteMeta struct {
	RequestID        string `json:"request_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Actor            string `json:"actor"`
}

type CreateProjectCommand struct {
	RequestID     string `json:"request_id"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	DurationMS    int64  `json:"duration_ms"`
	Language      string `json:"language"`
	MediaChecksum string `json:"media_checksum"`
	StyleProfile  string `json:"style_profile"`
	Assignee      string `json:"assignee"`
	Actor         string `json:"actor"`
}

type SaveCuesCommand struct {
	WriteMeta
	Cues []domain.CaptionCue `json:"cues"`
}
type ShiftCuesCommand struct {
	WriteMeta
	CueIDs          []string `json:"cue_ids"`
	OffsetMS        int64    `json:"offset_ms"`
	PreviewRevision int64    `json:"preview_revision"`
}
type SplitCueCommand struct {
	WriteMeta
	CueID           string `json:"cue_id,omitempty"`
	SplitTimeMS     int64  `json:"split_time_ms"`
	TextOffset      int    `json:"text_offset"`
	PreviewRevision int64  `json:"preview_revision"`
}
type MergeCuesCommand struct {
	WriteMeta
	CueIDs            []string `json:"cue_ids"`
	PreviewRevision   int64    `json:"preview_revision"`
	ConfirmationToken string   `json:"confirmation_token"`
	MergedSpeaker     string   `json:"merged_speaker"`
}
type CheckCommand struct{ WriteMeta }
type SubmitReviewCommand struct{ WriteMeta }
type AddFindingCommand struct {
	WriteMeta
	Finding  domain.ReviewFinding   `json:"finding"`
	Findings []domain.ReviewFinding `json:"findings"`
}
type ReviewDecisionCommand struct {
	WriteMeta
	Approved bool `json:"approved"`
}
type RemediateCommand struct {
	WriteMeta
	FindingID      string `json:"finding_id"`
	ResolutionNote string `json:"resolution_note"`
}
type SubmitReverificationCommand struct{ WriteMeta }
type VerifyFindingCommand struct {
	WriteMeta
	FindingID string `json:"finding_id"`
	Resolved  bool   `json:"resolved"`
}
type CompleteReverificationCommand struct{ WriteMeta }
type ApproveCommand struct {
	WriteMeta
	ApprovedBy        string `json:"approved_by"`
	PreviewRevision   int64  `json:"preview_revision"`
	CaptionChecksum   string `json:"caption_checksum"`
	ConfirmationToken string `json:"confirmation_token"`
}
type RemediateBatchCommand struct {
	WriteMeta
	Items []domain.RemediationItem `json:"items"`
}
type VerifyBatchCommand struct {
	WriteMeta
	Items []domain.VerificationItem `json:"items"`
}
type ConvertCheckFailuresCommand struct {
	WriteMeta
	CheckRunID string                         `json:"check_run_id"`
	Selections []domain.CheckFindingSelection `json:"selections"`
}
type RollbackCuesCommand struct {
	WriteMeta
	TargetRevision    int64  `json:"target_revision"`
	ConfirmationToken string `json:"confirmation_token"`
}

func (s *Service) CreateProject(ctx context.Context, cmd CreateProjectCommand) (*domain.MutationResult, bool, error) {
	if err := validateCreateMeta(cmd.RequestID, cmd.Actor); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(cmd.ID) == "" {
		cmd.ID = s.id("project")
	}
	project, err := domain.CreateProject(domain.NewProject{ID: cmd.ID, Title: cmd.Title, DurationMS: cmd.DurationMS, Language: cmd.Language, MediaChecksum: cmd.MediaChecksum, StyleProfile: cmd.StyleProfile, Assignee: cmd.Assignee}, s.now())
	if err != nil {
		return nil, false, err
	}
	if _, findErr := s.repo.FindByMediaChecksum(ctx, project.MediaChecksum); findErr == nil {
		// 仍进入同一建档事务：存储会先识别 request_id 重放，再由唯一约束
		// 对真正的重复请求返回原项目信息，且不会留下审计或幂等记录。
	} else if business, ok := findErr.(*domain.BusinessError); !ok || business.Code != domain.CodeNotFound {
		return nil, false, findErr
	}
	result, replay, err := s.repo.Create(ctx, project, cmd.RequestID, cmd.Actor)
	if err == nil {
		s.invalidateWorkbenchCache(project.ID)
	}
	return result, replay, err
}

func (s *Service) SaveCues(ctx context.Context, projectID string, cmd SaveCuesCommand) (*domain.MutationResult, bool, error) {
	ids := make([]string, len(cmd.Cues))
	for i := range cmd.Cues {
		ids[i] = cmd.Cues[i].ID
	}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "cues.saved", map[string]any{"cue_ids": ids}, func(p *domain.CaptionProject) (any, error) { return nil, p.SaveCues(cmd.Cues, s.now()) })
}

func (s *Service) PreviewRollback(ctx context.Context, projectID string, targetRevision, expectedRevision int64) (*domain.CaptionRollbackPreview, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, domain.Invalid("项目 ID 不能为空", "project_id")
	}
	if targetRevision <= 0 {
		return nil, domain.Invalid("target_revision 必须大于零", "target_revision")
	}
	if expectedRevision <= 0 {
		return nil, domain.Invalid("expected_revision 必须大于零", "expected_revision")
	}
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	target, checksum, err := s.repo.RevisionCues(ctx, projectID, targetRevision)
	if err != nil {
		return nil, err
	}
	return project.BuildRollbackPreview(targetRevision, expectedRevision, target, checksum)
}

func (s *Service) RollbackCues(ctx context.Context, projectID string, cmd RollbackCuesCommand) (*domain.MutationResult, bool, error) {
	if cmd.TargetRevision <= 0 {
		return nil, false, domain.Invalid("target_revision 必须大于零", "target_revision")
	}
	// Snapshots are immutable; loading before entering Mutate lets a missing or
	// corrupt target fail without creating an idempotency record or audit event.
	target, checksum, err := s.repo.RevisionCues(ctx, projectID, cmd.TargetRevision)
	if err != nil {
		return nil, false, err
	}
	detail := map[string]any{"source_revision": cmd.ExpectedRevision, "target_revision": cmd.TargetRevision}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "cues.rolled_back", detail, func(project *domain.CaptionProject) (any, error) {
		preview, err := project.BuildRollbackPreview(cmd.TargetRevision, cmd.ExpectedRevision, target, checksum)
		if err != nil {
			return nil, err
		}
		detail["field_changes"] = preview.Changes
		if err := project.ApplyRollback(cmd.TargetRevision, cmd.ExpectedRevision, target, checksum, cmd.ConfirmationToken, s.now()); err != nil {
			return nil, err
		}
		return preview, nil
	})
}

func (s *Service) PreviewCueShift(ctx context.Context, projectID string, cueIDs []string, offsetMS int64) (*domain.CueShiftPreview, error) {
	p, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return p.PreviewCueShift(cueIDs, offsetMS)
}

func (s *Service) ShiftCues(ctx context.Context, projectID string, cmd ShiftCuesCommand) (*domain.MutationResult, bool, error) {
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "cues.shifted", map[string]any{"shifted_count": len(cmd.CueIDs), "offset_ms": cmd.OffsetMS}, func(p *domain.CaptionProject) (any, error) {
		return p.ApplyCueShift(cmd.CueIDs, cmd.OffsetMS, cmd.PreviewRevision, s.now())
	})
}

func (s *Service) PreviewCueSplit(ctx context.Context, projectID, cueID string, splitTimeMS int64, textOffset int, expectedRevision int64) (*domain.CueSplitPreview, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, domain.Invalid("项目 ID 不能为空", "project_id")
	}
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return project.PreviewCueSplit(cueID, splitTimeMS, textOffset, expectedRevision)
}

func (s *Service) SplitCue(ctx context.Context, projectID, cueID string, cmd SplitCueCommand) (*domain.MutationResult, bool, error) {
	if strings.TrimSpace(cueID) == "" {
		cueID = cmd.CueID
	}
	detail := map[string]any{"source_cue_id": strings.TrimSpace(cueID), "split_time_ms": cmd.SplitTimeMS}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "cue.split", detail, func(project *domain.CaptionProject) (any, error) {
		beforeText := ""
		for _, cue := range project.Cues {
			if cue.ID == strings.TrimSpace(cueID) {
				beforeText = summarizeAuditText(cue.Text)
				break
			}
		}
		result, err := project.ApplyCueSplit(cueID, s.id("cue"), cmd.SplitTimeMS, cmd.TextOffset, cmd.PreviewRevision, s.now())
		if err != nil {
			return nil, err
		}
		detail["new_cue_id"] = result.NewCueID
		detail["before_text_summary"] = beforeText
		detail["first_text_summary"] = summarizeAuditText(result.First.Text)
		detail["second_text_summary"] = summarizeAuditText(result.Second.Text)
		return result, nil
	})
}

func (s *Service) PreviewCueMerge(ctx context.Context, projectID string, cueIDs []string, expectedRevision int64) (*domain.CueMergePreview, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, domain.Invalid("项目 ID 不能为空", "project_id")
	}
	project, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return project.PreviewCueMerge(cueIDs, expectedRevision)
}

func (s *Service) MergeCues(ctx context.Context, projectID string, cmd MergeCuesCommand) (*domain.MutationResult, bool, error) {
	detail := map[string]any{"cue_ids": cmd.CueIDs, "merged_speaker": strings.TrimSpace(cmd.MergedSpeaker)}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "cues.merged", detail, func(project *domain.CaptionProject) (any, error) {
		return project.ApplyCueMerge(cmd.CueIDs, cmd.MergedSpeaker, cmd.ExpectedRevision, cmd.PreviewRevision, cmd.ConfirmationToken, s.now())
	})
}

func summarizeAuditText(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= 80 {
		return string(runes)
	}
	return string(runes[:80]) + "…"
}

func (s *Service) RunChecks(ctx context.Context, projectID string, cmd CheckCommand) (*domain.MutationResult, bool, error) {
	return s.mutate(ctx, projectID, cmd.WriteMeta, "checks.completed", func(p *domain.CaptionProject) (any, error) {
		if p.Status == domain.StatusReleased {
			return nil, domain.Forbidden("已发布项目不可重新执行规则检查")
		}
		return p.RunChecksForRevision(s.id("checkrun"), p.Revision+1, s.now()), nil
	})
}

func (s *Service) SubmitReview(ctx context.Context, projectID string, cmd SubmitReviewCommand) (*domain.MutationResult, bool, error) {
	return s.mutate(ctx, projectID, cmd.WriteMeta, "review.submitted", func(p *domain.CaptionProject) (any, error) {
		return nil, p.SubmitReview(s.now())
	})
}

func (s *Service) AddFinding(ctx context.Context, projectID string, cmd AddFindingCommand) (*domain.MutationResult, bool, error) {
	findings := cmd.Findings
	if len(findings) == 0 && (cmd.Finding.CueID != "" || cmd.Finding.Description != "") {
		findings = []domain.ReviewFinding{cmd.Finding}
	}
	for i := range findings {
		if strings.TrimSpace(findings[i].ID) == "" {
			findings[i].ID = s.id("finding")
		}
	}
	ids := make([]string, len(findings))
	for i := range findings {
		ids[i] = findings[i].ID
	}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "findings.reported", map[string]any{"added_count": len(findings), "finding_ids": ids}, func(p *domain.CaptionProject) (any, error) {
		return findings, p.AddFindings(findings, s.now())
	})
}

func (s *Service) ReviewDecision(ctx context.Context, projectID string, cmd ReviewDecisionCommand) (*domain.MutationResult, bool, error) {
	event := "review.changes_requested"
	if cmd.Approved {
		event = "review.approved"
	}
	return s.mutate(ctx, projectID, cmd.WriteMeta, event, func(p *domain.CaptionProject) (any, error) { return nil, p.ReviewDecision(cmd.Approved, s.now()) })
}

func (s *Service) Remediate(ctx context.Context, projectID string, cmd RemediateCommand) (*domain.MutationResult, bool, error) {
	return s.mutate(ctx, projectID, cmd.WriteMeta, "finding.remediated", func(p *domain.CaptionProject) (any, error) {
		return nil, p.Remediate(cmd.FindingID, cmd.ResolutionNote, s.now())
	})
}
func (s *Service) RemediateBatch(ctx context.Context, projectID string, cmd RemediateBatchCommand) (*domain.MutationResult, bool, error) {
	ids := make([]string, len(cmd.Items))
	for i := range cmd.Items {
		ids[i] = cmd.Items[i].FindingID
	}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "findings.remediated_batch", map[string]any{"finding_count": len(cmd.Items), "finding_ids": ids}, func(p *domain.CaptionProject) (any, error) { return cmd.Items, p.RemediateBatch(cmd.Items, s.now()) })
}

func (s *Service) SubmitReverification(ctx context.Context, projectID string, cmd SubmitReverificationCommand) (*domain.MutationResult, bool, error) {
	detail := map[string]any{}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "reverification.submitted", detail, func(p *domain.CaptionProject) (any, error) {
		evidence := make([]map[string]any, 0, len(p.Findings))
		for _, finding := range p.Findings {
			if finding.Status == domain.FindingRemediated {
				evidence = append(evidence, map[string]any{"finding_id": finding.ID, "evidence_valid": finding.EvidenceValid, "reported_cue_revision": finding.ReportedCueRevision, "resolved_cue_revision": finding.ResolvedCueRevision})
			}
		}
		detail["evidence"] = evidence
		return nil, p.SubmitReverification(s.now())
	})
}

func (s *Service) VerifyFinding(ctx context.Context, projectID string, cmd VerifyFindingCommand) (*domain.MutationResult, bool, error) {
	event := "finding.rejected"
	if cmd.Resolved {
		event = "finding.resolved"
	}
	detail := map[string]any{"finding_id": cmd.FindingID, "reviewer": cmd.Actor}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, event, detail, func(p *domain.CaptionProject) (any, error) {
		if finding := findFinding(p.Findings, cmd.FindingID); finding != nil {
			detail["cue_revision"] = finding.ResolvedCueRevision
			detail["evidence_valid"] = finding.EvidenceValid
		}
		return nil, p.VerifyFinding(cmd.FindingID, cmd.Actor, cmd.Resolved, s.now())
	})
}
func (s *Service) VerifyBatch(ctx context.Context, projectID string, cmd VerifyBatchCommand) (*domain.MutationResult, bool, error) {
	detail := map[string]any{"finding_count": len(cmd.Items), "reviewer": cmd.Actor}
	return s.mutateDetailed(ctx, projectID, cmd.WriteMeta, "findings.verified_batch", detail, func(p *domain.CaptionProject) (any, error) {
		verified := make([]map[string]any, 0, len(cmd.Items))
		for _, item := range cmd.Items {
			if finding := findFinding(p.Findings, item.FindingID); finding != nil {
				verified = append(verified, map[string]any{"finding_id": item.FindingID, "evidence_valid": finding.EvidenceValid, "cue_revision": finding.ResolvedCueRevision})
			}
		}
		detail["verification"] = verified
		return cmd.Items, p.VerifyBatch(cmd.Items, cmd.Actor, s.now())
	})
}

func (s *Service) CompleteReverification(ctx context.Context, projectID string, cmd CompleteReverificationCommand) (*domain.MutationResult, bool, error) {
	return s.mutate(ctx, projectID, cmd.WriteMeta, "reverification.completed", func(p *domain.CaptionProject) (any, error) { return nil, p.CompleteReverification(s.now()) })
}

func (s *Service) Approve(ctx context.Context, projectID string, cmd ApproveCommand) (*domain.MutationResult, bool, error) {
	return s.mutate(ctx, projectID, cmd.WriteMeta, "release.approved", func(p *domain.CaptionProject) (any, error) {
		return p.ApprovePreview(cmd.ApprovedBy, s.id("manifest"), cmd.PreviewRevision, cmd.CaptionChecksum, cmd.ConfirmationToken, s.now())
	})
}

func (s *Service) ReleasePreview(ctx context.Context, projectID string) (*domain.ReleasePreview, error) {
	p, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	preview := p.ReleasePreview()
	return &preview, nil
}

func (s *Service) ManifestReport(ctx context.Context, projectID string) (*domain.ManifestReport, error) {
	p, err := s.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p.Status != domain.StatusReleased {
		return nil, domain.NotFound("发布清单", projectID)
	}
	manifest, err := s.repo.Manifest(ctx, projectID)
	if err != nil {
		return nil, err
	}
	p.Manifest = manifest
	events, err := s.repo.Audit(ctx, projectID, 0, 200)
	if err != nil {
		return nil, err
	}
	var approval *domain.AuditEvent
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "release.approved" {
			approval = &events[i]
			break
		}
	}
	return &domain.ManifestReport{Manifest: manifest, Integrity: domain.VerifyManifest(p, approval)}, nil
}

func (s *Service) GetProject(ctx context.Context, id string) (*domain.CaptionProject, error) {
	return s.repo.Get(ctx, id)
}
func (s *Service) ListProjects(ctx context.Context) ([]domain.ProjectSummary, error) {
	return s.repo.List(ctx)
}
func (s *Service) ProjectQueue(ctx context.Context, filter domain.QueueFilter) (*domain.ProjectQueue, error) {
	if filter.Risk != "" && !domain.ValidRiskLevel(filter.Risk) {
		return nil, domain.Invalid("不支持的风险值", "risk")
	}
	switch filter.Sort {
	case "", "risk_desc", "risk_asc", "severe_desc", "severe_asc", "updated_desc", "updated_asc":
	default:
		return nil, domain.Invalid("不支持的排序字段", "sort")
	}
	if filter.UpdatedFrom != nil && filter.UpdatedTo != nil && filter.UpdatedFrom.After(*filter.UpdatedTo) {
		return nil, domain.Invalid("更新时间范围无效", "updated_from", "updated_to")
	}
	items, stats, err := s.repo.ListFiltered(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &domain.ProjectQueue{Projects: items, Stats: stats}, nil
}
func (s *Service) Audit(ctx context.Context, id string, after int64, limit int) ([]domain.AuditEvent, error) {
	return s.repo.Audit(ctx, id, after, limit)
}
func (s *Service) AuditPage(ctx context.Context, id string, q domain.AuditQuery) (domain.AuditPage, error) {
	if q.After < 0 {
		return domain.AuditPage{}, domain.Invalid("after 必须是非负整数", "after")
	}
	if q.EventType != "" && !allowedAuditEvent(q.EventType) {
		return domain.AuditPage{}, domain.Invalid("不支持的事件类型", "event_type")
	}
	if len([]rune(q.Actor)) > 100 {
		return domain.AuditPage{}, domain.Invalid("actor 长度不能超过 100", "actor")
	}
	if q.From != nil && q.To != nil && q.From.After(*q.To) {
		return domain.AuditPage{}, domain.Invalid("时间范围无效", "from", "to")
	}
	if _, err := s.repo.Get(ctx, id); err != nil {
		return domain.AuditPage{}, err
	}
	return s.repo.AuditQuery(ctx, id, q)
}
func allowedAuditEvent(v string) bool {
	switch v {
	case "project.created", "cues.saved", "cues.shifted", "cue.split", "cues.rolled_back", "checks.completed", "review.submitted", "findings.reported", "findings.converted_from_checks", "finding.remediated", "findings.remediated_batch", "review.changes_requested", "reverification.submitted", "finding.resolved", "finding.rejected", "findings.verified_batch", "reverification.completed", "review.approved", "release.approved":
		return true
	}
	return false
}
func (s *Service) RevisionDiff(ctx context.Context, id string, from, to int64) (*domain.RevisionDiff, error) {
	if from <= 0 || to <= 0 || from >= to {
		return nil, domain.Invalid("修订范围必须为正向区间", "from", "to")
	}
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Status == domain.StatusReleased && to > p.Revision {
		return nil, domain.NotFound("项目修订", fmt.Sprint(to))
	}
	fc, fs, err := s.repo.RevisionCues(ctx, id, from)
	if err != nil {
		return nil, err
	}
	tc, ts, err := s.repo.RevisionCues(ctx, id, to)
	if err != nil {
		return nil, err
	}
	d := domain.CompareCueSnapshots(id, from, to, fc, tc, fs, ts)
	return &d, nil
}
func (s *Service) CheckHistory(ctx context.Context, id string, f domain.CheckHistoryFilter) (*domain.CheckHistorySummary, error) {
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return nil, domain.Invalid("时间范围无效", "from", "to")
	}
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &domain.CheckHistorySummary{Runs: []domain.CheckHistoryRun{}, RuleCounts: map[string]int{}}
	for _, run := range p.CheckRuns {
		if f.RunID != "" && run.ID != f.RunID {
			continue
		}
		if f.From != nil && run.RunAt.Before(*f.From) {
			continue
		}
		if f.To != nil && run.RunAt.After(*f.To) {
			continue
		}
		results := []domain.RuleCheck{}
		rulesSeen := map[string]bool{}
		for _, c := range run.Results {
			if f.Rule != "" && c.Rule != f.Rule || f.CueID != "" && c.CueID != f.CueID || f.Level != "" && c.Level != f.Level {
				continue
			}
			results = append(results, c)
			if !rulesSeen[c.Rule] {
				out.RuleCounts[c.Rule]++
				rulesSeen[c.Rule] = true
			}
			if c.Level == "warning" && !c.Passed {
				out.Warnings++
			}
		}
		if f.Rule != "" || f.CueID != "" || f.Level != "" {
			if len(results) == 0 {
				continue
			}
		}
		out.Runs = append(out.Runs, domain.CheckHistoryRun{ID: run.ID, ProjectRevision: run.ProjectRevision, RunAt: run.RunAt, Results: results, Current: run.ProjectRevision == p.Revision && run.ID == p.CheckRuns[len(p.CheckRuns)-1].ID})
	}
	out.RunCount = len(out.Runs)
	if len(out.Runs) > 0 {
		for _, c := range out.Runs[0].Results {
			if !c.Passed && c.Level == "error" {
				out.NewFailures++
			}
		}
	}
	for i := range out.Runs {
		if i > 0 {
			diff := domain.CheckRunDiff([]domain.RuleCheckRun{{Results: out.Runs[i-1].Results}, {Results: out.Runs[i].Results}})
			out.NewFailures += len(diff.NewFailures)
			out.PersistentFailures += len(diff.PersistentFailure)
			out.Fixed += len(diff.Fixed)
		}
	}
	return out, nil
}
func (s *Service) Ready(ctx context.Context) error { return s.repo.Ready(ctx) }

func (s *Service) mutate(ctx context.Context, projectID string, meta WriteMeta, event string, fn func(*domain.CaptionProject) (any, error)) (*domain.MutationResult, bool, error) {
	return s.mutateDetailed(ctx, projectID, meta, event, nil, fn)
}

func (s *Service) mutateDetailed(ctx context.Context, projectID string, meta WriteMeta, event string, detail map[string]any, fn func(*domain.CaptionProject) (any, error)) (*domain.MutationResult, bool, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, false, domain.Invalid("项目 ID 不能为空", "project_id")
	}
	if err := validateMeta(meta); err != nil {
		return nil, false, err
	}
	result, replay, err := s.repo.Mutate(ctx, domain.Mutation{ProjectID: projectID, ExpectedRevision: meta.ExpectedRevision, RequestID: meta.RequestID, EventType: event, Actor: meta.Actor, Detail: detail}, fn)
	if err == nil {
		s.invalidateWorkbenchCache(projectID)
	}
	return result, replay, err
}

func validateMeta(meta WriteMeta) error {
	requestID := strings.TrimSpace(meta.RequestID)
	if requestID == "" {
		return domain.Invalid("request_id 不能为空", "request_id")
	}
	if len(requestID) > 128 {
		return domain.Invalid("request_id 长度不能超过 128", "request_id")
	}
	if meta.ExpectedRevision <= 0 {
		return domain.Invalid("expected_revision 必须大于零", "expected_revision")
	}
	actor := strings.TrimSpace(meta.Actor)
	if actor == "" {
		return domain.Invalid("actor 不能为空", "actor")
	}
	if len([]rune(actor)) > 100 {
		return domain.Invalid("actor 长度不能超过 100", "actor")
	}
	return nil
}

func validateCreateMeta(requestID, actor string) error {
	return validateMeta(WriteMeta{RequestID: requestID, ExpectedRevision: 1, Actor: actor})
}

func randomID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return prefix + "-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return prefix + "-" + hex.EncodeToString(b)
}
