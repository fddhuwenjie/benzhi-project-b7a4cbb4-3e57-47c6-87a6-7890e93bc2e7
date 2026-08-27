package create_media_error_chain

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/domain"
	"caption-release-workbench/internal/store"
)

// wrappingRepository models a persistence adapter that adds operation context
// while preserving the underlying business error through %w.
type wrappingRepository struct {
	domain.Repository
}

func (r wrappingRepository) FindByMediaChecksum(context.Context, string) (*domain.ProjectSummary, error) {
	return nil, fmt.Errorf("素材基线预检: %w", domain.NotFound("素材基线", "checksum"))
}

func TestCreateProjectPreservesWrappedMediaNotFound(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "wrapped-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	service := application.New(wrappingRepository{Repository: repo})
	result, replay, err := service.CreateProject(context.Background(), application.CreateProjectCommand{
		RequestID:     "create-wrapped-media",
		ID:            "wrapped-project",
		Title:         "包装错误链节目",
		DurationMS:    10000,
		Language:      "zh-CN",
		MediaChecksum: "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		StyleProfile:  "规范",
		Assignee:      "制作员",
		Actor:         "制作员",
	})
	if err != nil || replay || result == nil || result.Project == nil {
		t.Fatalf("带上下文包装的素材未找到不应阻止建档: replay=%v err=%v result=%#v", replay, err, result)
	}
}
