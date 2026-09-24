package custom

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// This entire file is mostly taken from urfave/cli/v3's source, with the exception of suggestCommand which is
// modified for a nicer error message and stricter matching, and its helpers suggestionThreshold and withinOneEdit.

// jaroDistance is the measure of similarity between two strings. It returns a
// value between 0 and 1, where 1 indicates identical strings and 0 indicates
// completely different strings.
//
// Adapted from https://github.com/xrash/smetrics/blob/5f08fbb34913bc8ab95bb4f2a89a0637ca922666/jaro.go.
func jaroDistance(a, b string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	lenA := float64(len(a))
	lenB := float64(len(b))
	hashA := make([]bool, len(a))
	hashB := make([]bool, len(b))
	maxDistance := int(math.Max(0, math.Floor(math.Max(lenA, lenB)/2.0)-1))

	var matches float64
	for i := 0; i < len(a); i++ {
		start := int(math.Max(0, float64(i-maxDistance)))
		end := int(math.Min(lenB-1, float64(i+maxDistance)))

		for j := start; j <= end; j++ {
			if hashB[j] {
				continue
			}
			if a[i] == b[j] {
				hashA[i] = true
				hashB[j] = true
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return 0
	}

	var transpositions float64
	var j int
	for i := 0; i < len(a); i++ {
		if !hashA[i] {
			continue
		}
		for !hashB[j] {
			j++
		}
		if a[i] != b[j] {
			transpositions++
		}
		j++
	}

	transpositions /= 2
	return ((matches / lenA) + (matches / lenB) + ((matches - transpositions) / matches)) / 3.0
}

// jaroWinkler is more accurate when strings have a common prefix up to a
// defined maximum length.
//
// Adapted from https://github.com/xrash/smetrics/blob/5f08fbb34913bc8ab95bb4f2a89a0637ca922666/jaro-winkler.go.
func jaroWinkler(a, b string) float64 {
	const (
		boostThreshold = 0.7
		prefixSize     = 4
	)
	jaroDist := jaroDistance(a, b)
	if jaroDist <= boostThreshold {
		return jaroDist
	}

	prefix := int(math.Min(float64(len(a)), math.Min(float64(prefixSize), float64(len(b)))))

	var prefixMatch float64
	for i := 0; i < prefix; i++ {
		if a[i] == b[i] {
			prefixMatch++
		} else {
			break
		}
	}
	return jaroDist + 0.1*prefixMatch*(1.0-jaroDist)
}

// suggestionThreshold is the minimum jaro-winkler similarity required before we
// will print a "Did you mean" suggestion. Below this, the closest match is too
// dissimilar to be a useful guess, so we stay silent rather than mislead.
// 0.7 matches the boostThreshold used by jaroWinkler above — the prefix boost
// only kicks in past that, so it's a natural "plausibly the same word" cutoff.
// Names within one edit of the input are exempt; see suggestCommand.
const suggestionThreshold = 0.7

// withinOneEdit reports whether a and b differ by at most one insertion,
// deletion, substitution, or transposition of adjacent characters.
func withinOneEdit(a, b string) bool {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(a)-len(b) > 1 {
		return false
	}
	i := 0
	for i < len(b) && a[i] == b[i] {
		i++
	}
	if i == len(a) {
		return true
	}
	if len(a) != len(b) {
		return a[i+1:] == b[i:]
	}
	return a[i+1:] == b[i+1:] ||
		(i+1 < len(a) && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:])
}

// suggestCommand takes a list of commands and a provided string to suggest a
// command name, ignoring case. A name within one edit of the input is always
// suggested and outranks every other name, because jaro-winkler undervalues
// typos in short names ("rn" for "run") and misranks long names that share a
// prefix. Otherwise the closest name must exceed suggestionThreshold. Returns an
// empty string when no command is sufficiently similar; the upstream urfave/cli
// error formatter omits the suggestion clause in that case.
func suggestCommand(commands []*cli.Command, provided string) string {
	provided = strings.ToLower(provided)
	if provided == "" {
		return ""
	}
	// An exact 7/10 jaro score computes as 0.7000000000000001, so allow for
	// float error before counting the threshold as passed.
	distance := suggestionThreshold + 1e-9
	var lineage []*cli.Command
	for _, command := range commands {
		for _, name := range command.Names() {
			name = strings.ToLower(name)
			newDistance := jaroWinkler(name, provided)
			if withinOneEdit(name, provided) {
				newDistance++ // outranks every name further away
			}
			if newDistance > distance {
				distance = newDistance
				lineage = command.Lineage()
			}
		}
	}
	if lineage == nil {
		return ""
	}

	var parts []string
	for _, command := range lineage {
		parts = append(parts, command.Name)
	}
	slices.Reverse(parts)
	return fmt.Sprintf("Did you mean '%s'?", strings.Join(parts, " "))
}
