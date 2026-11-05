package updatenotice

import "strings"

// stableVersion accepts only a complete numeric release, with an optional v.
// Prerelease, development, and build metadata never become notice text.
func stableVersion(value string) (string, bool) {
	value = strings.TrimPrefix(value, "v")
	if len(value) == 0 || len(value) > 64 {
		return "", false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return "", false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return "", false
			}
		}
	}
	return value, true
}

// Numeric strings avoid machine-width overflow in remote version components.
func newerVersion(latest, current string) bool {
	a, b := strings.Split(latest, "."), strings.Split(current, ".")
	for index := range a {
		if len(a[index]) != len(b[index]) {
			return len(a[index]) > len(b[index])
		}
		if a[index] != b[index] {
			return a[index] > b[index]
		}
	}
	return false
}
