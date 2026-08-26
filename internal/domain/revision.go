package domain

// NextRevision computes the next optimistic-concurrency revision.
func NextRevision(current int64) int64 {
	if current < 1 {
		return 1
	}
	return current + 1
}
