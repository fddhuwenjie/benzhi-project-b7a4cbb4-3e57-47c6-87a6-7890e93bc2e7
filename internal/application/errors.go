package application

import (
	"errors"
	"fmt"
)

var ErrServiceUnavailable = errors.New("应用服务不可用")

type auditDetailLoadError struct {
	projectID string
	stage     string
	message   string
}

func newAuditDetailLoadError(projectID, stage string, err error) error {
	return &auditDetailLoadError{projectID: projectID, stage: stage, message: err.Error()}
}

func (e *auditDetailLoadError) Error() string {
	return fmt.Sprintf("加载项目 %s 的审计事件详情失败（%s）：%s", e.projectID, e.stage, e.message)
}
