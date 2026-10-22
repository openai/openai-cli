package commandsuggest

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Search only ranks declared candidates. It never completes or invents a command.
func searchCandidates(candidates []Candidate, query string) []int {
	words := strings.Fields(strings.ToLower(query))
	query = strings.Join(words, " ")
	type match struct {
		index int
		// Prefer command names, then arguments, then descriptions. Within each
		// tier prefer whole words, contiguous phrases, and shorter commands.
		rank [5]int
	}
	var matches []match
	for index, candidate := range candidates {
		if len(words) == 0 {
			matches = append(matches, match{index: index})
			continue
		}
		command := strings.ToLower(candidate.Command)
		path, arguments, hasFlags := strings.Cut(command, " -")
		if hasFlags {
			arguments = "-" + arguments
		}
		description := strings.Fields(strings.ToLower(candidate.Description))
		for i, word := range description {
			if label, _, link := strings.Cut(word, "]("); link {
				description[i] = label
			} else if strings.Contains(word, "://") || strings.Contains(word, "www.") {
				description[i] = ""
			}
		}
		prose := strings.Join(description, " ")
		entry := match{index: index}
		matched := true
		for _, word := range words {
			partial := searchWord(path, word)
			if partial < 0 {
				partial = searchWord(arguments, word)
				if partial >= 0 {
					entry.rank[1]++
				} else {
					partial = searchWord(prose, word)
					if partial == 1 && utf8.RuneCountInString(word) < 3 {
						partial = -1
					}
					entry.rank[0]++
				}
			}
			if partial < 0 {
				matched = false
				break
			}
			entry.rank[2] += partial
		}
		if !matched {
			continue
		}
		entry.rank[3] = 2
		if path == query || strings.HasSuffix(path, " "+query) {
			entry.rank[3] = 0
		} else if searchWord(path, query) >= 0 {
			entry.rank[3] = 1
		}
		if entry.rank[0] == 0 {
			entry.rank[4] = utf8.RuneCountInString(command)
		}
		matches = append(matches, entry)
	}
	slices.SortStableFunc(matches, func(a, b match) int {
		for i := range a.rank {
			if order := cmp.Compare(a.rank[i], b.rank[i]); order != 0 {
				return order
			}
		}
		return 0
	})
	var result []int
	for _, match := range matches {
		result = append(result, match.index)
	}
	return result
}

// Match whole words or word prefixes, including hyphenated command names.
// Return 0 for a whole word, 1 for a prefix, and -1 for no match.
func searchWord(text, word string) int {
	best := -1
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], word)
		if index < 0 {
			break
		}
		start := offset + index
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[start+len(word):])
		if start == 0 || !searchWordRune(before) {
			if start+len(word) == len(text) || !searchWordRune(after) {
				return 0
			}
			best = 1
		}
		offset = start + len(word)
	}
	return best
}

func searchWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}
