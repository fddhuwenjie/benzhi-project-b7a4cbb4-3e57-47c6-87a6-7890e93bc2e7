package domain

import "unicode/utf8"

// TextRuneCount is used by reading-speed checks and UI summaries.
func TextRuneCount(text string) int { return utf8.RuneCountInString(text) }
