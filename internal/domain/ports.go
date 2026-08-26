package domain

import "context"

type Mutation struct {
	ProjectID        string
	ExpectedRevision int64
	RequestID        string
	EventType        string
	Actor            string
	Detail           map[string]any
}

type MutationResult struct {
	Project *CaptionProject `json:"project,omitempty"`
	Value   any             `json:"value,omitempty"`
}

type Repository interface {
	Create(context.Context, *CaptionProject, string, string) (*MutationResult, bool, error)
	FindByMediaChecksum(context.Context, string) (*ProjectSummary, error)
	Mutate(context.Context, Mutation, func(*CaptionProject) (any, error)) (*MutationResult, bool, error)
	Get(context.Context, string) (*CaptionProject, error)
	List(context.Context) ([]ProjectSummary, error)
	ListFiltered(context.Context, QueueFilter) ([]ProjectSummary, QueueStats, error)
	Audit(context.Context, string, int64, int) ([]AuditEvent, error)
	AuditQuery(context.Context, string, AuditQuery) (AuditPage, error)
	AuditEvent(context.Context, string, int64) (*AuditEvent, error)
	RevisionCues(context.Context, string, int64) ([]CaptionCue, string, error)
	CueAtRevision(context.Context, string, string, int64) (*CaptionCue, error)
	Manifest(context.Context, string) (*ReleaseManifest, error)
	Ready(context.Context) error
	Close() error
}
