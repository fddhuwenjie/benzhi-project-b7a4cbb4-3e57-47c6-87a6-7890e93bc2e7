package domain

import "strings"

// NormalizeProjectID keeps identifiers stable across API boundaries.
func NormalizeProjectID(id string) string { return strings.TrimSpace(id) }
