package domain

// Audit event names are shared by storage and HTTP timeline views.
const (
	AuditProjectCreated  = "project.created"
	AuditCuesSaved       = "cues.saved"
	AuditReviewSubmitted = "review.submitted"
	AuditReleased        = "project.released"
)
