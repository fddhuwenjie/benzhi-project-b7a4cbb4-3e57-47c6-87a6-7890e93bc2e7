package audit_detail_corruption_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
	"caption-release-workbench/internal/store"
	_ "modernc.org/sqlite"
)

func TestAuditQueryRejectsMalformedDetail(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit-corruption.db")
	repo, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := domain.CreateProject(domain.NewProject{
		ID:            "project-audit-corruption",
		Title:         "审计损坏复现",
		DurationMS:    10_000,
		Language:      "zh-CN",
		MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StyleProfile:  "规范",
		Assignee:      "制作员",
	}, time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		repo.Close()
		t.Fatal(err)
	}
	if _, _, err := repo.Create(context.Background(), project, "create-audit-corruption", "制作员"); err != nil {
		repo.Close()
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	// Corrupt the persisted audit payload while keeping the row and all other
	// project state intact. A real restart then reads this durable bad record.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE audit_events SET detail_json=? WHERE project_id=?`, []byte("{malformed-json"), project.ID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	repo, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service := application.New(repo)
	page, err := service.AuditPage(context.Background(), project.ID, domain.AuditQuery{Limit: 10})
	if err == nil {
		t.Fatalf("AuditPage should reject malformed persisted detail, got page=%#v", page)
	}
}
