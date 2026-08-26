package domain

import "sort"

// SortCues returns a detached timeline ordered by start time.
func SortCues(cues []CaptionCue) []CaptionCue {
	out := append([]CaptionCue(nil), cues...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMS < out[j].StartMS })
	return out
}
