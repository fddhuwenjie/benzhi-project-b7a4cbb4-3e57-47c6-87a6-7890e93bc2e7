package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"caption-release-workbench/internal/domain"
)

func TestMutationIdempotencyConflictAndAudit(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	p, err := domain.CreateProject(domain.NewProject{ID: "p1", Title: "节目", DurationMS: 10000, Language: "zh-CN", MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", StyleProfile: "规范", Assignee: "甲"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, replay, err := repo.Create(ctx, p, "request-create", "甲"); err != nil || replay {
		t.Fatalf("创建失败: %v replay=%v", err, replay)
	}
	mutation := domain.Mutation{ProjectID: "p1", ExpectedRevision: 1, RequestID: "request-save", EventType: "cues.saved", Actor: "甲"}
	change := func(p *domain.CaptionProject) (any, error) {
		return nil, p.SaveCues([]domain.CaptionCue{{ID: "c1", StartMS: 0, EndMS: 2000, Speaker: "甲", Text: "内容"}}, time.Now())
	}
	first, replay, err := repo.Mutate(ctx, mutation, change)
	if err != nil || replay || first.Project.Revision != 2 {
		t.Fatalf("首次变更失败: %v replay=%v", err, replay)
	}
	second, replay, err := repo.Mutate(ctx, mutation, func(*domain.CaptionProject) (any, error) { return nil, errors.New("不应执行") })
	if err != nil || !replay || second.Project.Revision != 2 {
		t.Fatalf("幂等重放失败: %v replay=%v", err, replay)
	}
	conflicting := mutation
	conflicting.RequestID = "request-conflict"
	if _, _, err := repo.Mutate(ctx, conflicting, change); err == nil {
		t.Fatal("旧修订号应冲突")
	}
	loaded, err := repo.Get(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 2 || len(loaded.Cues) != 1 {
		t.Fatalf("冲突不应产生部分写入: %#v", loaded)
	}
	events, err := repo.Audit(ctx, "p1", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "project.created" || events[1].Type != "cues.saved" {
		t.Fatalf("审计事件异常: %#v", events)
	}
}

func TestConcurrentDuplicateMediaCreatesOnlyOneBaseline(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "duplicate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	checksum := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	projects := make([]*domain.CaptionProject, 2)
	for i, id := range []string{"p1", "p2"} {
		projects[i], err = domain.CreateProject(domain.NewProject{ID: id, Title: "节目" + id, DurationMS: 10000, Language: "zh-CN", MediaChecksum: checksum, StyleProfile: "规范", Assignee: "甲"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range projects {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, _, errs[index] = repo.Create(context.Background(), projects[index], "create-"+projects[index].ID, "甲")
		}(i)
	}
	wg.Wait()
	successes, conflicts := 0, 0
	for _, createErr := range errs {
		if createErr == nil {
			successes++
		} else {
			var business *domain.BusinessError
			if errors.As(createErr, &business) && business.Code == domain.CodeConflict && business.Details["project_id"] != nil {
				conflicts++
			}
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("并发建档结果异常: %#v", errs)
	}
	items, err := repo.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("项目数异常: %d %v", len(items), err)
	}
	var auditCount, idempotencyCount int
	if err := repo.db.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT count(*) FROM idempotency`).Scan(&idempotencyCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || idempotencyCount != 1 {
		t.Fatalf("失败请求留下写入: audit=%d idempotency=%d", auditCount, idempotencyCount)
	}
}
