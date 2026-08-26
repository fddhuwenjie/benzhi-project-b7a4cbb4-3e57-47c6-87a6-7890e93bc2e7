package application

// Request keys used for idempotent command execution.
const (
	RequestIDKey        = "request_id"
	ExpectedRevisionKey = "expected_revision"
)
