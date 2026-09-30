package forge

import (
	"fmt"
	"strings"
)

// ParseRepositoryFullName parses a canonical forge repository name into its
// namespace and repository components. The final slash separates the repository
// name so nested namespaces such as group/subgroup/repo remain intact.
func ParseRepositoryFullName(fullName string) (Repository, error) {
	fullName = strings.TrimSpace(fullName)
	separator := strings.LastIndex(fullName, "/")
	if separator <= 0 || separator == len(fullName)-1 {
		return Repository{}, fmt.Errorf("%w: invalid repository full name %q", ErrInvalidRequest, fullName)
	}
	return Repository{
		Namespace: fullName[:separator],
		Name:      fullName[separator+1:],
	}, nil
}
