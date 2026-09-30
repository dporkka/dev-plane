package scheduler

// NormalizeOwnershipPath exposes the scheduler's canonical repository-relative
// ownership normalization for adapters that need to persist admitted leases.
func NormalizeOwnershipPath(value string) (string, error) {
	return normalizeOwnership(value)
}
