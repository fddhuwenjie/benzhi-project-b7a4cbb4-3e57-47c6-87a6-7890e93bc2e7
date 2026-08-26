package application

// PageLimit bounds list responses returned to the browser.
const PageLimit = 100

func clampLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > PageLimit {
		return PageLimit
	}
	return limit
}
