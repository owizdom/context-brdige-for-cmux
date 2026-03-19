package parser

import (
	"regexp"
	"strings"
)

// extractMatches pulls file paths from regex capture groups and tags them with an operation.
func extractMatches(re *regexp.Regexp, s, op string) []FileChange {
	var changes []FileChange
	matches := re.FindAllStringSubmatch(s, -1)
	for _, m := range matches {
		if len(m) > 1 {
			path := strings.TrimSpace(m[1])
			path = strings.Trim(path, `"'`)
			if idx := strings.Index(path, ","); idx > 0 {
				path = strings.TrimSpace(path[:idx])
			}
			if path != "" {
				changes = append(changes, FileChange{Path: path, Op: op})
			}
		}
	}
	return changes
}

// deduplicateChanges removes duplicate file paths, upgrading "read" to a write
// operation if both appear.
func deduplicateChanges(changes []FileChange) []FileChange {
	seen := map[string]string{}
	var out []FileChange
	for _, c := range changes {
		prev, exists := seen[c.Path]
		if !exists {
			seen[c.Path] = c.Op
			out = append(out, c)
		} else if prev == "read" && c.Op != "read" {
			seen[c.Path] = c.Op
			for i, o := range out {
				if o.Path == c.Path {
					out[i].Op = c.Op
					break
				}
			}
		}
	}
	return out
}

// lastNLines returns the last n lines of s.
func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
