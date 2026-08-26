package domain

import "fmt"

type ErrorCode string

const (
	CodeInvalid   ErrorCode = "invalid"
	CodeNotFound  ErrorCode = "not_found"
	CodeConflict  ErrorCode = "conflict"
	CodeForbidden ErrorCode = "forbidden"
)

type BusinessError struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Fields  []string       `json:"fields,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *BusinessError) Error() string { return e.Message }

func Invalid(message string, fields ...string) error {
	return &BusinessError{Code: CodeInvalid, Message: message, Fields: fields}
}

func Conflict(message string) error {
	return &BusinessError{Code: CodeConflict, Message: message}
}

func ConflictWithDetails(message string, details map[string]any) error {
	return &BusinessError{Code: CodeConflict, Message: message, Details: details}
}

func Forbidden(message string) error {
	return &BusinessError{Code: CodeForbidden, Message: message}
}

func NotFound(kind, id string) error {
	return &BusinessError{Code: CodeNotFound, Message: fmt.Sprintf("%s %q 不存在", kind, id)}
}
