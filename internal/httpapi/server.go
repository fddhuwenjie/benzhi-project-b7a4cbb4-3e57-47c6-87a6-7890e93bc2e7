package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
)

type Server struct {
	service *application.Service
	logger  *slog.Logger
}

func New(service *application.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{service: service, logger: logger}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/readiness", s.ReadinessHandler)
	mux.HandleFunc("GET /api/projects", s.ListProjectsHandler)
	mux.HandleFunc("POST /api/projects", s.CreateProjectHandler)
	mux.HandleFunc("GET /api/projects/{projectID}", s.GetProjectHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/workbench", s.WorkbenchHandler)
	mux.HandleFunc("PUT /api/projects/{projectID}/cues", s.SaveCuesHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/rollback-preview", s.RollbackPreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/rollback/preview", s.RollbackPreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/rollback", s.RollbackHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/revisions/rollback-preview", s.RollbackPreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/revisions/rollback", s.RollbackHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/rollback-preview", s.RollbackPreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/rollback", s.RollbackHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/cues/search", s.SearchCuesHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/shift-preview", s.ShiftCuePreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/shift", s.ShiftCuesHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/{cueID}/split-preview", s.SplitCuePreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/{cueID}/split/preview", s.SplitCuePreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/{cueID}/split", s.SplitCueHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/split-preview", s.SplitCuePreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/split", s.SplitCueHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/merge-preview", s.MergeCuePreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/merge/preview", s.MergeCuePreviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/cues/merge", s.MergeCuesHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/checks", s.RunChecksHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/submit-review", s.SubmitReviewHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/findings", s.AddFindingHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/findings", s.FindingWorklistHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/findings/worklist", s.FindingWorklistHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/findings/reverification-summary", s.ReverificationEvidenceSummaryHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/findings/evidence-summary", s.ReverificationEvidenceSummaryHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/findings/reverification-evidence", s.ReverificationEvidenceSummaryHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/reverification/evidence-summary", s.ReverificationEvidenceSummaryHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/reverification/evidence", s.ReverificationEvidenceSummaryHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/findings/from-checks", s.ConvertCheckFailuresHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/findings/{findingID}/evidence", s.FindingEvidenceHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/review-decision", s.ReviewDecisionHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/remediate", s.RemediateHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/submit-reverification", s.SubmitReverificationHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/verify-finding", s.VerifyFindingHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/complete-reverification", s.CompleteReverificationHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/approve", s.ApproveHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/release-preview", s.ReleasePreviewHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/manifest", s.ManifestHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/verification-package", s.VerificationPackageHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/verification-package/summary", s.VerificationPackageSummaryHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/verification-package/download", s.VerificationPackageHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/audit", s.AuditHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/audit/{eventID}", s.AuditEventDetailHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/revisions/diff", s.RevisionDiffHandler)
	mux.HandleFunc("GET /api/projects/{projectID}/checks/history", s.CheckHistoryHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/findings/remediate-batch", s.RemediateBatchHandler)
	mux.HandleFunc("POST /api/projects/{projectID}/findings/verify-batch", s.VerifyBatchHandler)
}

func (s *Server) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()
	if err := s.service.Ready(ctx); err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) ListProjectsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := domain.QueueFilter{Language: strings.TrimSpace(q.Get("language")), Assignee: strings.TrimSpace(q.Get("assignee")), Risk: domain.RiskLevel(strings.TrimSpace(q.Get("risk"))), Sort: strings.TrimSpace(q.Get("sort"))}
	if sortBy := strings.TrimSpace(q.Get("sort_by")); sortBy != "" {
		order := strings.TrimSpace(q.Get("order"))
		if order == "" {
			order = "desc"
		}
		mapping := map[string]string{"risk": "risk_" + order, "severe_findings": "severe_" + order, "updated_at": "updated_" + order}
		var ok bool
		filter.Sort, ok = mapping[sortBy]
		if !ok || order != "asc" && order != "desc" {
			s.writeError(w, domain.Invalid("不支持的排序字段", "sort_by", "order"))
			return
		}
	}
	raw := strings.TrimSpace(q.Get("status"))
	if raw != "" {
		filter.Status = domain.ProjectStatus(raw)
		if !domain.ValidProjectStatus(filter.Status) {
			s.writeError(w, domain.Invalid("不支持的项目状态", "status"))
			return
		}
	}
	if len([]rune(filter.Language)) > 100 {
		s.writeError(w, domain.Invalid("language 长度不能超过 100", "language"))
		return
	}
	if len([]rune(filter.Assignee)) > 100 {
		s.writeError(w, domain.Invalid("assignee 长度不能超过 100", "assignee"))
		return
	}
	parseQueueTime := func(key string) (*time.Time, error) {
		value := strings.TrimSpace(q.Get(key))
		if value == "" {
			return nil, nil
		}
		parsed, err := time.Parse(time.RFC3339, value)
		return &parsed, err
	}
	var timeErr error
	filter.UpdatedFrom, timeErr = parseQueueTime("updated_from")
	if timeErr != nil {
		s.writeError(w, domain.Invalid("无法解析更新时间起点", "updated_from"))
		return
	}
	filter.UpdatedTo, timeErr = parseQueueTime("updated_to")
	if timeErr != nil {
		s.writeError(w, domain.Invalid("无法解析更新时间终点", "updated_to"))
		return
	}
	queue, err := s.service.ProjectQueue(r.Context(), filter)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, queue)
}

func (s *Server) SearchCuesHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := domain.CueSearchQuery{Keyword: q.Get("keyword")}
	parseOptional := func(key string) (*int64, error) {
		raw := strings.TrimSpace(q.Get(key))
		if raw == "" {
			return nil, nil
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
	var err error
	query.StartMS, err = parseOptional("start_ms")
	if err != nil {
		s.writeError(w, domain.Invalid("起始时间必须是整数", "start_ms"))
		return
	}
	query.EndMS, err = parseOptional("end_ms")
	if err != nil {
		s.writeError(w, domain.Invalid("结束时间必须是整数", "end_ms"))
		return
	}
	if raw := strings.TrimSpace(q.Get("expected_revision")); raw != "" {
		query.ExpectedRevision, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || query.ExpectedRevision <= 0 {
			s.writeError(w, domain.Invalid("expected_revision 必须大于零", "expected_revision"))
			return
		}
	}
	result, err := s.service.SearchCues(r.Context(), r.PathValue("projectID"), query)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) CreateProjectHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.CreateProjectCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.CreateProject(r.Context(), cmd)
	s.writeMutation(w, http.StatusCreated, result, replay, err)
}

func (s *Server) GetProjectHandler(w http.ResponseWriter, r *http.Request) {
	project, err := s.service.GetProject(r.Context(), r.PathValue("projectID"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) WorkbenchHandler(w http.ResponseWriter, r *http.Request) {
	view, err := s.service.GetWorkbench(r.Context(), r.PathValue("projectID"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) SaveCuesHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.SaveCuesCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.SaveCues(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

type rollbackPreviewRequest struct {
	TargetRevision   int64 `json:"target_revision"`
	ExpectedRevision int64 `json:"expected_revision"`
}

func (s *Server) RollbackPreviewHandler(w http.ResponseWriter, r *http.Request) {
	var request rollbackPreviewRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.TargetRevision <= 0 {
		s.writeError(w, domain.Invalid("target_revision 必须大于零", "target_revision"))
		return
	}
	if request.ExpectedRevision <= 0 {
		s.writeError(w, domain.Invalid("expected_revision 必须大于零", "expected_revision"))
		return
	}
	preview, err := s.service.PreviewRollback(r.Context(), r.PathValue("projectID"), request.TargetRevision, request.ExpectedRevision)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) RollbackHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.RollbackCuesCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.RollbackCues(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

type shiftPreviewRequest struct {
	CueIDs   []string `json:"cue_ids"`
	OffsetMS int64    `json:"offset_ms"`
}

type splitPreviewRequest struct {
	CueID            string `json:"cue_id,omitempty"`
	SplitTimeMS      int64  `json:"split_time_ms"`
	TextOffset       int    `json:"text_offset"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type mergePreviewRequest struct {
	CueIDs           []string `json:"cue_ids"`
	ExpectedRevision int64    `json:"expected_revision"`
}

func (s *Server) ShiftCuePreviewHandler(w http.ResponseWriter, r *http.Request) {
	var request shiftPreviewRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	preview, err := s.service.PreviewCueShift(r.Context(), r.PathValue("projectID"), request.CueIDs, request.OffsetMS)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) ShiftCuesHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.ShiftCuesCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.ShiftCues(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) SplitCuePreviewHandler(w http.ResponseWriter, r *http.Request) {
	var request splitPreviewRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	cueID := strings.TrimSpace(r.PathValue("cueID"))
	if cueID == "" {
		cueID = strings.TrimSpace(request.CueID)
	}
	if cueID == "" {
		s.writeError(w, domain.Invalid("字幕段 ID 不能为空", "cue_id"))
		return
	}
	if request.ExpectedRevision <= 0 {
		s.writeError(w, domain.Invalid("expected_revision 必须大于零", "expected_revision"))
		return
	}
	preview, err := s.service.PreviewCueSplit(r.Context(), r.PathValue("projectID"), cueID, request.SplitTimeMS, request.TextOffset, request.ExpectedRevision)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) SplitCueHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.SplitCueCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	cueID := strings.TrimSpace(r.PathValue("cueID"))
	if cueID == "" {
		cueID = strings.TrimSpace(cmd.CueID)
	}
	if cueID == "" {
		s.writeError(w, domain.Invalid("字幕段 ID 不能为空", "cue_id"))
		return
	}
	result, replay, err := s.service.SplitCue(r.Context(), r.PathValue("projectID"), cueID, cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) MergeCuePreviewHandler(w http.ResponseWriter, r *http.Request) {
	var request mergePreviewRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 {
		s.writeError(w, domain.Invalid("expected_revision 必须大于零", "expected_revision"))
		return
	}
	preview, err := s.service.PreviewCueMerge(r.Context(), r.PathValue("projectID"), request.CueIDs, request.ExpectedRevision)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) MergeCuesHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.MergeCuesCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.MergeCues(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) RunChecksHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.CheckCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.RunChecks(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) SubmitReviewHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.SubmitReviewCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.SubmitReview(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) AddFindingHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.AddFindingCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.AddFinding(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusCreated, result, replay, err)
}

func (s *Server) ConvertCheckFailuresHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.ConvertCheckFailuresCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.ConvertCheckFailures(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusCreated, result, replay, err)
}

func (s *Server) FindingEvidenceHandler(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("expected_revision")), 10, 64)
	if err != nil || revision <= 0 {
		s.writeError(w, domain.Invalid("expected_revision 必须大于零", "expected_revision"))
		return
	}
	result, err := s.service.FindingEvidence(r.Context(), r.PathValue("projectID"), r.PathValue("findingID"), revision)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) FindingWorklistHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	revision, err := strconv.ParseInt(strings.TrimSpace(q.Get("expected_revision")), 10, 64)
	if err != nil || revision <= 0 {
		s.writeError(w, domain.Invalid("expected_revision 必须大于零", "expected_revision"))
		return
	}
	splitValues := func(key string) []string {
		values := []string{}
		for _, raw := range q[key] {
			for _, part := range strings.Split(raw, ",") {
				if value := strings.TrimSpace(part); value != "" {
					values = append(values, value)
				}
			}
		}
		return values
	}
	query := domain.FindingWorklistQuery{CueID: strings.TrimSpace(q.Get("cue_id")), Keyword: strings.TrimSpace(q.Get("keyword")), Sort: strings.TrimSpace(q.Get("sort")), ExpectedRevision: revision, Severities: splitValues("severity"), Categories: splitValues("category")}
	switch query.Sort {
	case "severity", "severity_priority":
		query.Sort = "severity_desc"
	case "timeline", "cue_time", "cue_time_asc":
		query.Sort = "timeline_asc"
	case "last_verified", "last_verified_desc", "recently_verified":
		query.Sort = "verified_desc"
	}
	for _, value := range splitValues("status") {
		query.Statuses = append(query.Statuses, domain.FindingStatus(value))
	}
	if len([]rune(query.CueID)) > 128 {
		s.writeError(w, domain.Invalid("cue_id 长度不能超过 128", "cue_id"))
		return
	}
	if len([]rune(query.Keyword)) > 100 {
		s.writeError(w, domain.Invalid("关键词长度不能超过 100", "keyword"))
		return
	}
	result, err := s.service.FindingWorklist(r.Context(), r.PathValue("projectID"), query)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) ReverificationEvidenceSummaryHandler(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("expected_revision")), 10, 64)
	if err != nil || revision <= 0 {
		s.writeError(w, domain.Invalid("expected_revision 必须大于零", "expected_revision"))
		return
	}
	result, err := s.service.ReverificationEvidenceSummary(r.Context(), r.PathValue("projectID"), revision)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) ReviewDecisionHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.ReviewDecisionCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.ReviewDecision(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) RemediateHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.RemediateCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.Remediate(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) SubmitReverificationHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.SubmitReverificationCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.SubmitReverification(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) VerifyFindingHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.VerifyFindingCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.VerifyFinding(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) CompleteReverificationHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.CompleteReverificationCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.CompleteReverification(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) ApproveHandler(w http.ResponseWriter, r *http.Request) {
	var cmd application.ApproveCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}
	result, replay, err := s.service.Approve(r.Context(), r.PathValue("projectID"), cmd)
	s.writeMutation(w, http.StatusOK, result, replay, err)
}

func (s *Server) ReleasePreviewHandler(w http.ResponseWriter, r *http.Request) {
	preview, err := s.service.ReleasePreview(r.Context(), r.PathValue("projectID"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) ManifestHandler(w http.ResponseWriter, r *http.Request) {
	report, err := s.service.ManifestReport(r.Context(), r.PathValue("projectID"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) VerificationPackageSummaryHandler(w http.ResponseWriter, r *http.Request) {
	s.writeVerificationPackageSummary(w, r)
}

func (s *Server) VerificationPackageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("mode") == "summary" || r.URL.Query().Get("summary") == "true" {
		s.writeVerificationPackageSummary(w, r)
		return
	}
	pack, err := s.service.VerificationPackage(r.Context(), r.PathValue("projectID"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	filename := safeFilenamePart(pack.ProjectID) + "-manifest-" + safeFilenamePart(pack.ManifestVersion) + "-verification.json"
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, pack)
}

func (s *Server) writeVerificationPackageSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.service.VerificationPackageSummary(r.Context(), r.PathValue("projectID"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func safeFilenamePart(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('-')
		}
	}
	value = strings.Trim(builder.String(), ".-")
	if value == "" {
		return "caption-project"
	}
	return value
}

func (s *Server) AuditHandler(w http.ResponseWriter, r *http.Request) {
	after, err := parseNonnegative(r.URL.Query().Get("after"), 0)
	if err != nil {
		s.writeError(w, domain.Invalid("after 必须是非负整数", "after"))
		return
	}
	limit, err := parseNonnegative(r.URL.Query().Get("limit"), 50)
	if err != nil {
		s.writeError(w, domain.Invalid("limit 必须是非负整数", "limit"))
		return
	}
	q := domain.AuditQuery{After: after, Limit: int(limit), Actor: strings.TrimSpace(r.URL.Query().Get("actor")), EventType: strings.TrimSpace(r.URL.Query().Get("event_type"))}
	parseTime := func(key string) (*time.Time, error) {
		v := strings.TrimSpace(r.URL.Query().Get(key))
		if v == "" {
			return nil, nil
		}
		t, e := time.Parse(time.RFC3339, v)
		return &t, e
	}
	var e error
	q.From, e = parseTime("from")
	if e != nil {
		s.writeError(w, domain.Invalid("无法解析起始时间", "from"))
		return
	}
	q.To, e = parseTime("to")
	if e != nil {
		s.writeError(w, domain.Invalid("无法解析结束时间", "to"))
		return
	}
	page, err := s.service.AuditPage(r.Context(), r.PathValue("projectID"), q)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) AuditEventDetailHandler(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(r.PathValue("eventID"), 10, 64)
	if err != nil || eventID <= 0 {
		s.writeError(w, domain.Invalid("event_id 必须大于零", "event_id"))
		return
	}
	revision := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("revision")); raw != "" {
		revision, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || revision <= 0 {
			s.writeError(w, domain.Invalid("revision 必须大于零", "revision"))
			return
		}
	}
	result, err := s.service.AuditEventDetail(r.Context(), r.PathValue("projectID"), eventID, strings.TrimSpace(r.URL.Query().Get("include")), revision)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) RevisionDiffHandler(w http.ResponseWriter, r *http.Request) {
	from, e := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	if e != nil {
		from = 0
	}
	to, e := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
	if e != nil {
		to = 0
	}
	d, err := s.service.RevisionDiff(r.Context(), r.PathValue("projectID"), from, to)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
func (s *Server) CheckHistoryHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.CheckHistoryFilter{RunID: q.Get("run_id"), Rule: q.Get("rule"), CueID: q.Get("cue_id"), Level: q.Get("level")}
	if f.Level != "" && f.Level != "error" && f.Level != "warning" {
		s.writeError(w, domain.Invalid("结果级别无效", "level"))
		return
	}
	parse := func(k string) (*time.Time, error) {
		v := q.Get(k)
		if v == "" {
			return nil, nil
		}
		t, e := time.Parse(time.RFC3339, v)
		return &t, e
	}
	var e error
	f.From, e = parse("from")
	if e != nil {
		s.writeError(w, domain.Invalid("无法解析起始时间", "from"))
		return
	}
	f.To, e = parse("to")
	if e != nil {
		s.writeError(w, domain.Invalid("无法解析结束时间", "to"))
		return
	}
	v, err := s.service.CheckHistory(r.Context(), r.PathValue("projectID"), f)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (s *Server) RemediateBatchHandler(w http.ResponseWriter, r *http.Request) {
	var c application.RemediateBatchCommand
	if !decodeJSON(w, r, &c) {
		return
	}
	v, replay, err := s.service.RemediateBatch(r.Context(), r.PathValue("projectID"), c)
	s.writeMutation(w, http.StatusOK, v, replay, err)
}
func (s *Server) VerifyBatchHandler(w http.ResponseWriter, r *http.Request) {
	var c application.VerifyBatchCommand
	if !decodeJSON(w, r, &c) {
		return
	}
	v, replay, err := s.service.VerifyBatch(r.Context(), r.PathValue("projectID"), c)
	s.writeMutation(w, http.StatusOK, v, replay, err)
}

func (s *Server) writeMutation(w http.ResponseWriter, status int, result *domain.MutationResult, replay bool, err error) {
	if err != nil {
		s.writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotent-Replay", "true")
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	var business *domain.BusinessError
	if errors.As(err, &business) {
		status := http.StatusBadRequest
		switch business.Code {
		case domain.CodeNotFound:
			status = http.StatusNotFound
		case domain.CodeConflict:
			status = http.StatusConflict
		case domain.CodeForbidden:
			status = http.StatusForbidden
		case domain.CodeCorrupt:
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]any{"error": business})
		return
	}
	s.logger.Error("HTTP 请求失败", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "internal", "message": "服务内部错误"}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if media := r.Header.Get("Content-Type"); media != "" && !strings.HasPrefix(media, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": map[string]string{"code": "invalid", "message": "Content-Type 必须为 application/json"}})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid", "message": "JSON 请求体无效：" + cleanDecodeError(err)}})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid", "message": "请求体只能包含一个 JSON 对象"}})
		return false
	}
	return true
}

func cleanDecodeError(err error) string {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("第 %d 字节附近语法错误", syntax.Offset)
	}
	if errors.Is(err, io.EOF) {
		return "请求体为空"
	}
	return err.Error()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func parseNonnegative(raw string, fallback int) (int64, error) {
	if raw == "" {
		return int64(fallback), nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("invalid")
	}
	return value, nil
}
