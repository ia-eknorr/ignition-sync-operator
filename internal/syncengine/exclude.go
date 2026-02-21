package syncengine

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// hardcodedExcludes are always enforced regardless of user config.
var hardcodedExcludes = []string{
	"**/.sync-staging/**",
}

// protectedPatterns are paths in the destination that must never be deleted or overwritten.
var protectedPatterns = []string{
	"**/.resources/**",
	"**/.resources",
}

// MergeExcludes combines user-provided excludes with hardcoded ones, deduplicating.
func MergeExcludes(userExcludes []string) []string {
	seen := make(map[string]bool, len(userExcludes)+len(hardcodedExcludes))
	result := make([]string, 0, len(userExcludes)+len(hardcodedExcludes))

	for _, p := range userExcludes {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		result = append(result, p)
	}
	for _, p := range hardcodedExcludes {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

// ShouldExclude checks if a relative path matches any exclude pattern.
func ShouldExclude(relPath string, excludes []string) bool {
	// Normalize to forward slashes for consistent matching.
	relPath = filepath.ToSlash(relPath)

	for _, pattern := range excludes {
		pattern = filepath.ToSlash(pattern)
		matched, err := doublestar.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// IsProtected checks if a relative path is in the protected set (e.g. .resources/).
func IsProtected(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	for _, pattern := range protectedPatterns {
		matched, err := doublestar.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}
	}
	return false
}
