package cmd

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// This entire file is mostly taken from urfave/cli/v3's source, with the exception of suggestCommand which is
// modified for a nicer error message.

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

// suggestCommand takes a list of commands and a provided string to suggest a
// command name
func suggestCommand(commands []*cli.Command, provided string) string {
	distance := 0.0
	var lineage []*cli.Command
	for _, command := range commands {
		for _, name := range command.Names() {
			newDistance := jaroWinkler(name, provided)
			if newDistance > distance {
				distance = newDistance
				lineage = command.Lineage()
			}
		}
	}

	var parts []string
	for _, command := range lineage {
		parts = append(parts, command.Name)
	}
	slices.Reverse(parts)
	return fmt.Sprintf("Did you mean '%s'?", strings.Join(parts, " "))
}

func init() {
	cli.SuggestCommand = suggestCommand
}
