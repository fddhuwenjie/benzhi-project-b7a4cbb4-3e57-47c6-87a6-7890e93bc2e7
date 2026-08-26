package domain

import "strings"

// NormalizeCueID keeps cue identifiers stable across API boundaries.
func NormalizeCueID(id string) string { return strings.TrimSpace(id) }
