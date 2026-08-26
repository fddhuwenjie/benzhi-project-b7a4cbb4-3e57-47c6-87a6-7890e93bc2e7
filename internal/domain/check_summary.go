package domain

// CheckSummary counts rule outcomes without exposing persistence details.
type CheckSummary struct{ Total, Errors, Warnings int }

func SummarizeChecks(checks []RuleCheck) CheckSummary {
	result := CheckSummary{Total: len(checks)}
	for _, check := range checks {
		if check.Passed {
			continue
		}
		if check.Level == "warning" {
			result.Warnings++
		} else {
			result.Errors++
		}
	}
	return result
}
