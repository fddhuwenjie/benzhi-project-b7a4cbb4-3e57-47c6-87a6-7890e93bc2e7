package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"caption-release-workbench/internal/domain"
	_ "modernc.org/sqlite"
)

type SQLite struct{ db *sql.DB }

func Open(path string) (*SQLite, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS projects (
            id TEXT PRIMARY KEY, title TEXT NOT NULL, language TEXT NOT NULL,
            assignee TEXT NOT NULL, media_checksum TEXT NOT NULL UNIQUE, status TEXT NOT NULL, revision INTEGER NOT NULL,
            updated_at TEXT NOT NULL, aggregate_json BLOB NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS audit_events (
            id INTEGER PRIMARY KEY AUTOINCREMENT, project_id TEXT NOT NULL,
            event_type TEXT NOT NULL, actor TEXT NOT NULL, revision INTEGER NOT NULL,
            detail_json BLOB NOT NULL, created_at TEXT NOT NULL,
            FOREIGN KEY(project_id) REFERENCES projects(id)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_audit_project_cursor ON audit_events(project_id, id)`,
		`CREATE TABLE IF NOT EXISTS caption_cues (
            project_id TEXT NOT NULL, id TEXT NOT NULL, sequence INTEGER NOT NULL,
            start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, speaker TEXT NOT NULL,
            text TEXT NOT NULL, sound_description TEXT NOT NULL, cue_revision INTEGER NOT NULL,
            PRIMARY KEY(project_id,id), FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
        )`,
		`CREATE INDEX IF NOT EXISTS idx_cues_timeline ON caption_cues(project_id,sequence,start_ms)`,
		`CREATE TABLE IF NOT EXISTS rule_checks (
            project_id TEXT NOT NULL, id TEXT NOT NULL, cue_id TEXT NOT NULL,
            rule TEXT NOT NULL, level TEXT NOT NULL, message TEXT NOT NULL,
            passed INTEGER NOT NULL, checked_at TEXT NOT NULL,
            PRIMARY KEY(project_id,id), FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS rule_check_runs (
            project_id TEXT NOT NULL, id TEXT NOT NULL, project_revision INTEGER NOT NULL,
            run_at TEXT NOT NULL, results_json BLOB NOT NULL,
            PRIMARY KEY(project_id,id), FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS review_findings (
            project_id TEXT NOT NULL, id TEXT NOT NULL, cue_id TEXT NOT NULL,
            category TEXT NOT NULL, severity TEXT NOT NULL, description TEXT NOT NULL,
            status TEXT NOT NULL, reported_by TEXT NOT NULL, resolution_note TEXT NOT NULL,
            reported_cue_revision INTEGER NOT NULL, resolved_cue_revision INTEGER NOT NULL,
            evidence_valid INTEGER NOT NULL, verified_by TEXT NOT NULL, verified_at TEXT,
			review_history_json BLOB NOT NULL, source_check_run_id TEXT NOT NULL DEFAULT '',
			source_rule TEXT NOT NULL DEFAULT '', source_check_revision INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY(project_id,id), FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
        )`,
		`CREATE INDEX IF NOT EXISTS idx_findings_status ON review_findings(project_id,status)`,
		`CREATE TABLE IF NOT EXISTS release_manifests (
            id TEXT PRIMARY KEY, project_id TEXT NOT NULL UNIQUE, project_revision INTEGER NOT NULL,
            cue_count INTEGER NOT NULL, caption_checksum TEXT NOT NULL, media_checksum TEXT NOT NULL,
            approved_by TEXT NOT NULL, approved_at TEXT NOT NULL, manifest_version TEXT NOT NULL,
            FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS idempotency (
            request_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, operation_key TEXT NOT NULL,
            result_json BLOB NOT NULL, created_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS revision_snapshots (project_id TEXT NOT NULL, revision INTEGER NOT NULL, checksum TEXT NOT NULL, cues_json BLOB NOT NULL, PRIMARY KEY(project_id,revision), FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("初始化 SQLite: %w", err)
		}
	}
	if exists, err := columnExists(ctx, s.db, "projects", "media_checksum"); err != nil {
		return err
	} else if !exists {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN media_checksum TEXT`); err != nil {
			return fmt.Errorf("迁移素材校验值: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE projects SET media_checksum=lower(json_extract(aggregate_json,'$.media_checksum'))`); err != nil {
			return fmt.Errorf("回填素材校验值: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_media_checksum ON projects(media_checksum)`); err != nil {
		return fmt.Errorf("初始化素材校验值唯一索引: %w", err)
	}
	for _, migration := range []struct{ column, definition string }{
		{"reported_cue_revision", "INTEGER NOT NULL DEFAULT 1"},
		{"evidence_valid", "INTEGER NOT NULL DEFAULT 0"},
		{"review_history_json", "BLOB NOT NULL DEFAULT '[]'"},
		{"source_check_run_id", "TEXT NOT NULL DEFAULT ''"},
		{"source_rule", "TEXT NOT NULL DEFAULT ''"},
		{"source_check_revision", "INTEGER NOT NULL DEFAULT 0"},
	} {
		exists, err := columnExists(ctx, s.db, "review_findings", migration.column)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.ExecContext(ctx, `ALTER TABLE review_findings ADD COLUMN `+migration.column+` `+migration.definition); err != nil {
				return fmt.Errorf("迁移审校问题字段 %s: %w", migration.column, err)
			}
		}
	}
	return nil
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *SQLite) Close() error { return s.db.Close() }
func (s *SQLite) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&result); err != nil {
		return fmt.Errorf("SQLite 完整性检查失败: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("SQLite 完整性检查结果: %s", result)
	}
	var orphanCount int
	err := s.db.QueryRowContext(ctx, `SELECT
        (SELECT count(*) FROM caption_cues c LEFT JOIN projects p ON p.id=c.project_id WHERE p.id IS NULL) +
        (SELECT count(*) FROM rule_checks c LEFT JOIN projects p ON p.id=c.project_id WHERE p.id IS NULL) +
		(SELECT count(*) FROM rule_check_runs c LEFT JOIN projects p ON p.id=c.project_id WHERE p.id IS NULL) +
        (SELECT count(*) FROM review_findings f LEFT JOIN projects p ON p.id=f.project_id WHERE p.id IS NULL) +
        (SELECT count(*) FROM release_manifests m LEFT JOIN projects p ON p.id=m.project_id WHERE p.id IS NULL)`).Scan(&orphanCount)
	if err != nil {
		return fmt.Errorf("检查关联记录: %w", err)
	}
	if orphanCount != 0 {
		return fmt.Errorf("检测到 %d 条孤立关联记录", orphanCount)
	}
	return nil
}

func (s *SQLite) Create(ctx context.Context, project *domain.CaptionProject, requestID, actor string) (*domain.MutationResult, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedCreateResult(ctx, tx, requestID); err != nil {
		return nil, false, err
	} else if ok {
		return cached, true, nil
	}
	data, err := json.Marshal(project)
	if err != nil {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO projects(id,title,language,assignee,media_checksum,status,revision,updated_at,aggregate_json) VALUES(?,?,?,?,?,?,?,?,?)`, project.ID, project.Title, project.Language, project.Assignee, project.MediaChecksum, project.Status, project.Revision, project.UpdatedAt.Format(time.RFC3339Nano), data)
	if err != nil {
		if isConstraint(err) {
			if existing, lookupErr := findByMediaChecksum(ctx, tx, project.MediaChecksum); lookupErr == nil {
				return nil, false, duplicateMediaError(existing)
			}
			return nil, false, domain.Conflict("项目 ID 已存在")
		}
		return nil, false, err
	}
	result := &domain.MutationResult{Project: project}
	if err := syncChildren(ctx, tx, project); err != nil {
		return nil, false, err
	}
	if err := appendAudit(ctx, tx, project, "project.created", actor, requestID, nil); err != nil {
		return nil, false, err
	}
	if err := saveSnapshot(ctx, tx, project); err != nil {
		return nil, false, err
	}
	if err := cacheResult(ctx, tx, requestID, project.ID, "project.created", result); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return cloneResult(result), false, nil
}

func cachedCreateResult(ctx context.Context, tx *sql.Tx, requestID string) (*domain.MutationResult, bool, error) {
	var data []byte
	var operation string
	err := tx.QueryRowContext(ctx, `SELECT operation_key,result_json FROM idempotency WHERE request_id=?`, requestID).Scan(&operation, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if operation != "project.created" {
		return nil, false, domain.Conflict("request_id 已用于其他操作")
	}
	var result domain.MutationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false, err
	}
	return &result, true, nil
}

func (s *SQLite) Mutate(ctx context.Context, mutation domain.Mutation, change func(*domain.CaptionProject) (any, error)) (*domain.MutationResult, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if cached, ok, err := cachedResult(ctx, tx, mutation.RequestID, mutation.ProjectID, mutation.EventType); err != nil {
		return nil, false, err
	} else if ok {
		return cached, true, nil
	}
	project, err := loadProject(ctx, tx, mutation.ProjectID)
	if err != nil {
		return nil, false, err
	}
	if project.Revision != mutation.ExpectedRevision {
		return nil, false, domain.Conflict(fmt.Sprintf("修订冲突：当前为 %d，提交为 %d", project.Revision, mutation.ExpectedRevision))
	}
	value, err := change(project)
	if err != nil {
		return nil, false, err
	}
	oldRevision := project.Revision
	project.Revision++
	data, err := json.Marshal(project)
	if err != nil {
		return nil, false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE projects SET title=?,language=?,assignee=?,media_checksum=?,status=?,revision=?,updated_at=?,aggregate_json=? WHERE id=? AND revision=?`, project.Title, project.Language, project.Assignee, project.MediaChecksum, project.Status, project.Revision, project.UpdatedAt.Format(time.RFC3339Nano), data, project.ID, oldRevision)
	if err != nil {
		return nil, false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if rows != 1 {
		return nil, false, domain.Conflict("项目已被其他操作更新")
	}
	if err := syncChildren(ctx, tx, project); err != nil {
		return nil, false, err
	}
	if err := saveSnapshot(ctx, tx, project); err != nil {
		return nil, false, err
	}
	result := &domain.MutationResult{Project: project, Value: value}
	if err := appendAudit(ctx, tx, project, mutation.EventType, mutation.Actor, mutation.RequestID, mutation.Detail); err != nil {
		return nil, false, err
	}
	if err := cacheResult(ctx, tx, mutation.RequestID, project.ID, mutation.EventType, result); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return cloneResult(result), false, nil
}

func (s *SQLite) Get(ctx context.Context, id string) (*domain.CaptionProject, error) {
	row := s.db.QueryRowContext(ctx, `SELECT aggregate_json FROM projects WHERE id=?`, id)
	var data []byte
	if err := row.Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("项目", id)
	} else if err != nil {
		return nil, err
	}
	var project domain.CaptionProject
	if err := json.Unmarshal(data, &project); err != nil {
		return nil, fmt.Errorf("解析项目聚合: %w", err)
	}
	if err := domain.ValidateRestoredProject(&project); err != nil {
		return nil, fmt.Errorf("项目聚合完整性校验失败: %w", err)
	}
	return &project, nil
}

func (s *SQLite) List(ctx context.Context) ([]domain.ProjectSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,language,assignee,status,revision,updated_at,aggregate_json FROM projects ORDER BY updated_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ProjectSummary{}
	for rows.Next() {
		var item domain.ProjectSummary
		var updated string
		var data []byte
		if err := rows.Scan(&item.ID, &item.Title, &item.Language, &item.Assignee, &item.Status, &item.Revision, &updated, &data); err != nil {
			return nil, err
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		var p domain.CaptionProject
		if json.Unmarshal(data, &p) == nil {
			for _, c := range p.Checks {
				if !c.Passed && c.Level == "error" {
					item.FailedRuleCount++
				}
			}
			for _, f := range p.Findings {
				if f.Status != domain.FindingResolved {
					item.OpenFindingCount++
				}
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLite) ListFiltered(ctx context.Context, filter domain.QueueFilter) ([]domain.ProjectSummary, domain.QueueStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,language,assignee,status,revision,updated_at,aggregate_json FROM projects`)
	if err != nil {
		return nil, domain.QueueStats{}, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	base := []domain.ProjectSummary{}
	for rows.Next() {
		var item domain.ProjectSummary
		var updated string
		var data []byte
		if err := rows.Scan(&item.ID, &item.Title, &item.Language, &item.Assignee, &item.Status, &item.Revision, &updated, &data); err != nil {
			return nil, domain.QueueStats{}, err
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		if filter.Status != "" && item.Status != filter.Status || filter.Language != "" && item.Language != filter.Language || filter.Assignee != "" && item.Assignee != filter.Assignee || filter.UpdatedFrom != nil && item.UpdatedAt.Before(*filter.UpdatedFrom) || filter.UpdatedTo != nil && item.UpdatedAt.After(*filter.UpdatedTo) {
			continue
		}
		var project domain.CaptionProject
		if err := json.Unmarshal(data, &project); err != nil {
			return nil, domain.QueueStats{}, fmt.Errorf("解析项目聚合: %w", err)
		}
		item.Risk = domain.CalculateProjectRisk(&project, now)
		item.FailedRuleCount = item.Risk.FailedRuleCount
		item.OpenFindingCount = item.Risk.OpenFindingCount
		item.SevereFindingCount = item.Risk.SevereFindingCount
		base = append(base, item)
	}
	if err := rows.Err(); err != nil {
		return nil, domain.QueueStats{}, err
	}
	stats := domain.QueueStats{StatusCounts: map[domain.ProjectStatus]int{}, RiskCounts: map[domain.RiskLevel]int{}}
	filtered := make([]domain.ProjectSummary, 0, len(base))
	for _, item := range base {
		stats.StatusCounts[item.Status]++
		stats.RiskCounts[item.Risk.Level]++
		stats.FailedRuleCount += item.FailedRuleCount
		stats.OpenFindingCount += item.OpenFindingCount
		stats.SevereFindingCount += item.SevereFindingCount
		if stats.LatestUpdatedAt == nil || item.UpdatedAt.After(*stats.LatestUpdatedAt) {
			t := item.UpdatedAt
			stats.LatestUpdatedAt = &t
		}
		if filter.Risk != "" && item.Risk.Level != filter.Risk {
			continue
		}
		filtered = append(filtered, item)
	}
	riskRank := func(level domain.RiskLevel) int {
		return map[domain.RiskLevel]int{domain.RiskLow: 1, domain.RiskMedium: 2, domain.RiskHigh: 3}[level]
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		switch filter.Sort {
		case "risk_asc":
			if riskRank(a.Risk.Level) != riskRank(b.Risk.Level) {
				return riskRank(a.Risk.Level) < riskRank(b.Risk.Level)
			}
		case "severe_desc":
			if a.SevereFindingCount != b.SevereFindingCount {
				return a.SevereFindingCount > b.SevereFindingCount
			}
		case "severe_asc":
			if a.SevereFindingCount != b.SevereFindingCount {
				return a.SevereFindingCount < b.SevereFindingCount
			}
		case "updated_asc":
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.Before(b.UpdatedAt)
			}
		case "updated_desc":
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.After(b.UpdatedAt)
			}
		default:
			if riskRank(a.Risk.Level) != riskRank(b.Risk.Level) {
				return riskRank(a.Risk.Level) > riskRank(b.Risk.Level)
			}
			if a.SevereFindingCount != b.SevereFindingCount {
				return a.SevereFindingCount > b.SevereFindingCount
			}
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.Before(b.UpdatedAt)
			}
		}
		return a.ID < b.ID
	})
	return filtered, stats, nil
}

func (s *SQLite) FindByMediaChecksum(ctx context.Context, checksum string) (*domain.ProjectSummary, error) {
	return findByMediaChecksum(ctx, s.db, checksum)
}

func findByMediaChecksum(ctx context.Context, q rowQuerier, checksum string) (*domain.ProjectSummary, error) {
	var item domain.ProjectSummary
	var updated string
	err := q.QueryRowContext(ctx, `SELECT id,title,language,assignee,status,revision,updated_at FROM projects WHERE media_checksum=?`, checksum).Scan(&item.ID, &item.Title, &item.Language, &item.Assignee, &item.Status, &item.Revision, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("素材基线", checksum)
	}
	if err != nil {
		return nil, err
	}
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &item, nil
}

func duplicateMediaError(existing *domain.ProjectSummary) error {
	return domain.ConflictWithDetails("该素材已建立项目", map[string]any{"project_id": existing.ID, "title": existing.Title, "status": existing.Status, "assignee": existing.Assignee})
}

func (s *SQLite) Audit(ctx context.Context, projectID string, after int64, limit int) ([]domain.AuditEvent, error) {
	page, err := s.AuditQuery(ctx, projectID, domain.AuditQuery{After: after, Limit: limit})
	return page.Events, err
}

func (s *SQLite) AuditQuery(ctx context.Context, projectID string, q domain.AuditQuery) (domain.AuditPage, error) {
	if q.After > 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM audit_events WHERE project_id=? AND id=?`, projectID, q.After).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return domain.AuditPage{}, domain.Invalid("事件游标不存在", "after")
		} else if err != nil {
			return domain.AuditPage{}, err
		}
	}
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if q.Limit > 0 && q.Limit <= 200 {
		limit = q.Limit
	}
	args := []any{projectID, q.After}
	where := ` WHERE project_id=? AND id>?`
	if q.Actor != "" {
		where += ` AND actor=?`
		args = append(args, q.Actor)
	}
	if q.EventType != "" {
		where += ` AND event_type=?`
		args = append(args, q.EventType)
	}
	if q.From != nil {
		where += ` AND created_at>=?`
		args = append(args, q.From.UTC().Format(time.RFC3339Nano))
	}
	if q.To != nil {
		where += ` AND created_at<=?`
		args = append(args, q.To.UTC().Format(time.RFC3339Nano))
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id,event_type,actor,revision,detail_json,created_at FROM audit_events`+where+` ORDER BY id LIMIT ?`, args...)
	if err != nil {
		return domain.AuditPage{}, err
	}
	defer rows.Close()
	items := []domain.AuditEvent{}
	for rows.Next() {
		var item domain.AuditEvent
		var detail []byte
		var created string
		item.ProjectID = projectID
		if err := rows.Scan(&item.ID, &item.Type, &item.Actor, &item.Revision, &detail, &created); err != nil {
			return domain.AuditPage{}, err
		}
		if err := json.Unmarshal(detail, &item.Detail); err != nil {
			// 损坏的历史载荷被跳过，避免单行数据中止整页读取；汇总查询仍会计入该行。
			continue
		}
		parsedAt, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			// 时间戳损坏时同样跳过该事件，汇总仍会保留数据库计数。
			continue
		}
		item.CreatedAt = parsedAt
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.AuditPage{}, err
	}
	if err := rows.Close(); err != nil {
		return domain.AuditPage{}, err
	}
	page := domain.AuditPage{Events: items, Summary: domain.AuditSummary{ByEventType: map[string]int{}, ByActor: map[string]int{}}}
	hasMore := len(items) > limit
	if hasMore {
		page.Events = items[:limit]
		page.Summary.NextAfter = page.Events[len(page.Events)-1].ID
	}
	allArgs := []any{projectID}
	allWhere := ` WHERE project_id=?`
	if q.Actor != "" {
		allWhere += ` AND actor=?`
		allArgs = append(allArgs, q.Actor)
	}
	if q.EventType != "" {
		allWhere += ` AND event_type=?`
		allArgs = append(allArgs, q.EventType)
	}
	if q.From != nil {
		allWhere += ` AND created_at>=?`
		allArgs = append(allArgs, q.From.UTC().Format(time.RFC3339Nano))
	}
	if q.To != nil {
		allWhere += ` AND created_at<=?`
		allArgs = append(allArgs, q.To.UTC().Format(time.RFC3339Nano))
	}
	summaryRows, summaryErr := s.db.QueryContext(ctx, `SELECT event_type,actor,count(*) FROM audit_events`+allWhere+` GROUP BY event_type,actor`, allArgs...)
	if summaryErr != nil {
		return domain.AuditPage{}, summaryErr
	}
	defer summaryRows.Close()
	for summaryRows.Next() {
		var event, actor string
		var count int
		if err := summaryRows.Scan(&event, &actor, &count); err != nil {
			return domain.AuditPage{}, err
		}
		page.Summary.ByEventType[event] += count
		page.Summary.ByActor[actor] += count
	}
	if err := summaryRows.Err(); err != nil {
		return domain.AuditPage{}, err
	}
	return page, rows.Err()
}

func (s *SQLite) AuditEvent(ctx context.Context, projectID string, eventID int64) (*domain.AuditEvent, error) {
	var event domain.AuditEvent
	var detail []byte
	var created string
	event.ProjectID = projectID
	err := s.db.QueryRowContext(ctx, `SELECT id,event_type,actor,revision,detail_json,created_at FROM audit_events WHERE project_id=? AND id=?`, projectID, eventID).Scan(&event.ID, &event.Type, &event.Actor, &event.Revision, &detail, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("审计事件", fmt.Sprint(eventID))
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(detail, &event.Detail)
	event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &event, nil
}

func saveSnapshot(ctx context.Context, tx *sql.Tx, project *domain.CaptionProject) error {
	data, err := json.Marshal(project.Cues)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO revision_snapshots(project_id,revision,checksum,cues_json) VALUES(?,?,?,?)`, project.ID, project.Revision, project.CaptionChecksum(), data)
	return err
}

func (s *SQLite) RevisionCues(ctx context.Context, projectID string, revision int64) ([]domain.CaptionCue, string, error) {
	var data []byte
	var checksum string
	err := s.db.QueryRowContext(ctx, `SELECT cues_json,checksum FROM revision_snapshots WHERE project_id=? AND revision=?`, projectID, revision).Scan(&data, &checksum)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", domain.NotFound("项目修订", fmt.Sprintf("%d", revision))
	}
	if err != nil {
		return nil, "", err
	}
	var cues []domain.CaptionCue
	if err := json.Unmarshal(data, &cues); err != nil {
		return nil, "", err
	}
	return cues, checksum, nil
}

func (s *SQLite) CueAtRevision(ctx context.Context, projectID, cueID string, cueRevision int64) (*domain.CaptionCue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cues_json FROM revision_snapshots WHERE project_id=? ORDER BY revision`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var cues []domain.CaptionCue
		if err := json.Unmarshal(data, &cues); err != nil {
			return nil, err
		}
		for _, cue := range cues {
			if cue.ID == cueID && cue.CueRevision == cueRevision {
				copyCue := cue
				return &copyCue, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, domain.NotFound("字幕段历史快照", fmt.Sprintf("%s@%d", cueID, cueRevision))
}

func (s *SQLite) Manifest(ctx context.Context, projectID string) (*domain.ReleaseManifest, error) {
	var m domain.ReleaseManifest
	var approved string
	err := s.db.QueryRowContext(ctx, `SELECT id,project_id,project_revision,cue_count,caption_checksum,media_checksum,approved_by,approved_at,manifest_version FROM release_manifests WHERE project_id=?`, projectID).Scan(&m.ID, &m.ProjectID, &m.ProjectRevision, &m.CueCount, &m.CaptionChecksum, &m.MediaChecksum, &m.ApprovedBy, &approved, &m.ManifestVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("发布清单", projectID)
	}
	if err != nil {
		return nil, err
	}
	m.ApprovedAt, _ = time.Parse(time.RFC3339Nano, approved)
	return &m, nil
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadProject(ctx context.Context, q rowQuerier, id string) (*domain.CaptionProject, error) {
	var data []byte
	if err := q.QueryRowContext(ctx, `SELECT aggregate_json FROM projects WHERE id=?`, id).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("项目", id)
	} else if err != nil {
		return nil, err
	}
	var project domain.CaptionProject
	if err := json.Unmarshal(data, &project); err != nil {
		return nil, err
	}
	if err := domain.ValidateRestoredProject(&project); err != nil {
		return nil, fmt.Errorf("项目聚合完整性校验失败: %w", err)
	}
	return &project, nil
}

func cachedResult(ctx context.Context, tx *sql.Tx, requestID, projectID, operation string) (*domain.MutationResult, bool, error) {
	var data []byte
	var storedProject, storedOperation string
	err := tx.QueryRowContext(ctx, `SELECT project_id,operation_key,result_json FROM idempotency WHERE request_id=?`, requestID).Scan(&storedProject, &storedOperation, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if storedProject != projectID || storedOperation != operation {
		return nil, false, domain.Conflict("request_id 已用于其他项目或操作")
	}
	var result domain.MutationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false, err
	}
	return &result, true, nil
}

func cacheResult(ctx context.Context, tx *sql.Tx, requestID, projectID, operation string, result *domain.MutationResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO idempotency(request_id,project_id,operation_key,result_json,created_at) VALUES(?,?,?,?,?)`, requestID, projectID, operation, data, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func syncChildren(ctx context.Context, tx *sql.Tx, project *domain.CaptionProject) error {
	for _, table := range []string{"caption_cues", "rule_checks", "review_findings"} {
		statement := `DELETE FROM ` + table + ` WHERE project_id=?`
		if _, err := tx.ExecContext(ctx, statement, project.ID); err != nil {
			return fmt.Errorf("清理 %s 关联记录: %w", table, err)
		}
	}
	for _, cue := range project.Cues {
		_, err := tx.ExecContext(ctx, `INSERT INTO caption_cues(project_id,id,sequence,start_ms,end_ms,speaker,text,sound_description,cue_revision) VALUES(?,?,?,?,?,?,?,?,?)`, project.ID, cue.ID, cue.Sequence, cue.StartMS, cue.EndMS, cue.Speaker, cue.Text, cue.SoundDescription, cue.CueRevision)
		if err != nil {
			return fmt.Errorf("保存字幕段 %s: %w", cue.ID, err)
		}
	}
	for _, check := range project.Checks {
		_, err := tx.ExecContext(ctx, `INSERT INTO rule_checks(project_id,id,cue_id,rule,level,message,passed,checked_at) VALUES(?,?,?,?,?,?,?,?)`, project.ID, check.ID, check.CueID, check.Rule, check.Level, check.Message, check.Passed, check.CheckedAt.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("保存规则检查 %s: %w", check.ID, err)
		}
	}
	for _, run := range project.CheckRuns {
		results, err := json.Marshal(run.Results)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO rule_check_runs(project_id,id,project_revision,run_at,results_json) VALUES(?,?,?,?,?)`, project.ID, run.ID, run.ProjectRevision, run.RunAt.Format(time.RFC3339Nano), results)
		if err != nil {
			return fmt.Errorf("保存规则检查运行 %s: %w", run.ID, err)
		}
	}
	for _, finding := range project.Findings {
		var verifiedAt any
		if finding.VerifiedAt != nil {
			verifiedAt = finding.VerifiedAt.Format(time.RFC3339Nano)
		}
		history, err := json.Marshal(finding.ReviewHistory)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO review_findings(project_id,id,cue_id,category,severity,description,status,reported_by,resolution_note,reported_cue_revision,resolved_cue_revision,evidence_valid,verified_by,verified_at,review_history_json,source_check_run_id,source_rule,source_check_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, project.ID, finding.ID, finding.CueID, finding.Category, finding.Severity, finding.Description, finding.Status, finding.ReportedBy, finding.ResolutionNote, finding.ReportedCueRevision, finding.ResolvedCueRevision, finding.EvidenceValid, finding.VerifiedBy, verifiedAt, history, finding.SourceCheckRunID, finding.SourceRule, finding.SourceCheckRevision)
		if err != nil {
			return fmt.Errorf("保存审校问题 %s: %w", finding.ID, err)
		}
	}
	if project.Manifest == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM release_manifests WHERE project_id=?`, project.ID)
		return err
	}
	m := project.Manifest
	_, err := tx.ExecContext(ctx, `INSERT INTO release_manifests(id,project_id,project_revision,cue_count,caption_checksum,media_checksum,approved_by,approved_at,manifest_version) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id) DO UPDATE SET id=excluded.id,project_revision=excluded.project_revision,cue_count=excluded.cue_count,caption_checksum=excluded.caption_checksum,media_checksum=excluded.media_checksum,approved_by=excluded.approved_by,approved_at=excluded.approved_at,manifest_version=excluded.manifest_version`, m.ID, project.ID, m.ProjectRevision, m.CueCount, m.CaptionChecksum, m.MediaChecksum, m.ApprovedBy, m.ApprovedAt.Format(time.RFC3339Nano), m.ManifestVersion)
	if err != nil {
		return fmt.Errorf("保存发布清单: %w", err)
	}
	return nil
}

func appendAudit(ctx context.Context, tx *sql.Tx, project *domain.CaptionProject, eventType, actor, requestID string, extra map[string]any) error {
	values := map[string]any{"status": project.Status, "cue_count": len(project.Cues), "finding_count": len(project.Findings), "request_id": requestID, "operation": eventType, "project_revision": project.Revision}
	for key, value := range extra {
		values[key] = value
	}
	detail, _ := json.Marshal(values)
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(project_id,event_type,actor,revision,detail_json,created_at) VALUES(?,?,?,?,?,?)`, project.ID, eventType, actor, project.Revision, detail, project.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func cloneResult(input *domain.MutationResult) *domain.MutationResult {
	data, _ := json.Marshal(input)
	var output domain.MutationResult
	_ = json.Unmarshal(data, &output)
	return &output
}

func isConstraint(err error) bool {
	return err != nil && (contains(err.Error(), "constraint") || contains(err.Error(), "UNIQUE"))
}
func contains(value, fragment string) bool {
	for i := 0; i+len(fragment) <= len(value); i++ {
		if value[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
