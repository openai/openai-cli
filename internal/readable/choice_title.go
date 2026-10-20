package readable

import "strings"

// ChoiceTitle renders the existing compact label for a command's selected value.
func ChoiceTitle(value string) string {
	if value == "xhigh" {
		return "Extra high"
	}
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
