package domain

// StatusLabel returns the Chinese display label for a project status.
func StatusLabel(status ProjectStatus) string {
	labels := map[ProjectStatus]string{StatusDraft: "草稿", StatusInReview: "审校中", StatusChanges: "整改中", StatusReverification: "定向复验", StatusReady: "待发布", StatusReleased: "已发布"}
	if label, ok := labels[status]; ok {
		return label
	}
	return string(status)
}
