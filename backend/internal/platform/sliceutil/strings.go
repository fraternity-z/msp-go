package sliceutil

import "strings"

// CloneStrings copies values while preserving the repository/application DTO convention that nil becomes an empty slice.
func CloneStrings(values []string) []string {
	return append([]string{}, values...)
}

// AppendUniqueNonEmptyStrings returns trimmed non-empty strings in first-seen order across values and extras.
func AppendUniqueNonEmptyStrings(values []string, extras ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(extras))
	result := make([]string, 0, len(values)+len(extras))
	for _, batch := range [2][]string{values, extras} {
		for _, value := range batch {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
