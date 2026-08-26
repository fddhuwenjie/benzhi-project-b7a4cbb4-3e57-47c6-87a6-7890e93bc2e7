package domain

// FindingSeverityLabel returns a human-readable severity label.
func FindingSeverityLabel(severity string) string {
	labels := map[string]string{"minor": "一般", "major": "重要", "critical": "严重"}
	if label, ok := labels[severity]; ok {
		return label
	}
	return severity
}
