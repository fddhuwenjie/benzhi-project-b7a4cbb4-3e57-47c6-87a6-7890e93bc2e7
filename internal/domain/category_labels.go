package domain

// FindingCategoryLabel normalizes category names for the UI.
func FindingCategoryLabel(category string) string {
	labels := map[string]string{"accuracy": "准确性", "timing": "时序", "accessibility": "无障碍", "style": "规范"}
	if label, ok := labels[category]; ok {
		return label
	}
	return category
}
