package commandsuggest

import (
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// Built on the first miss and reused across keystrokes. Only installed routes
// contribute terms. Shell history, option values and example operands do not.
type helpIndex struct {
	postings     map[string][]helpHit
	basePostings map[string][]helpHit // Shared by the option-help tier.
	terms        []string             // sorted for prefix lookup
	scores       []helpScore
}

type helpHit struct {
	index int
	cost  int
}

type helpScore struct {
	words, total, lastCost int
	anchor                 bool // At least one query word names the route, its summary or an option.
}

// A lower cost is stronger evidence of the command's purpose.
const (
	routeWord = iota
	summaryWord
	optionWord
	helpWord
	interfaceWord
)

func newHelpIndex(candidates []Candidate, entries []searchEntry) *helpIndex {
	h := &helpIndex{postings: make(map[string][]helpHit), scores: make([]helpScore, len(candidates))}
	for i, candidate := range candidates {
		cmd := candidate.definition
		if cmd == nil {
			continue
		}

		h.add(candidate.path, i, routeWord)
		for _, alias := range cmd.Aliases {
			if safeCatalogName(alias) {
				h.add(strings.ToLower(alias), i, routeWord)
			}
		}
		h.add(entries[i].prose, i, summaryWord)
		summary, detailText := candidate.help.Command(cmd, candidate.path)
		if summary != cmd.Usage && summary != candidate.Description {
			h.add(searchableProse(summary), i, summaryWord)
		}
		if cmd.Usage != candidate.Description {
			h.add(searchableProse(cmd.Usage), i, summaryWord)
		}
		for _, flag := range cmd.Flags {
			if visible, ok := flag.(cli.VisibleFlag); ok && visible.IsVisible() {
				for _, name := range flag.Names() {
					h.add(strings.ToLower(name), i, optionWord)
				}
			}
		}
		var prose strings.Builder
		for line := range strings.SplitSeq(detailText, "\n") {
			// Indented shell examples are already catalogued separately.
			if line != "" && line[0] != ' ' && line[0] != '\t' {
				prose.WriteString(line)
				prose.WriteByte(' ')
			}
		}
		detail := searchableProse(prose.String())
		h.add(detail, i, helpWord)
		if describesEditor(detail) {
			h.add("interactive composer editor studio workbench launch open", i, interfaceWord)
		}
	}
	slices.Sort(h.terms)
	return h
}

// Option descriptions add weak evidence to the existing vocabulary. Keep their
// postings separate so subsequent primary searches still see only primary terms.
func (h *helpIndex) withOptionHelp(candidates []Candidate) *helpIndex {
	options := &helpIndex{
		postings: make(map[string][]helpHit), basePostings: h.postings,
		terms: slices.Clone(h.terms), scores: h.scores,
	}
	for i, candidate := range candidates {
		if candidate.definition == nil {
			continue
		}
		for _, flag := range candidate.definition.Flags {
			if visible, ok := flag.(cli.VisibleFlag); ok && visible.IsVisible() {
				options.add(searchableProse(candidate.help.Flag(candidate.definition, flag)), i, helpWord)
			}
		}
	}
	slices.Sort(options.terms)
	return options
}

func (h *helpIndex) add(text string, index, cost int) {
	for word := range strings.FieldsFuncSeq(text, func(r rune) bool { return !searchWordRune(r) }) {
		hits := h.postings[word]
		if len(hits) == 0 {
			if _, exists := h.basePostings[word]; !exists {
				h.terms = append(h.terms, word)
			}
		} else if last := &hits[len(hits)-1]; last.index == index {
			last.cost = min(last.cost, cost)
			continue
		}
		h.postings[word] = append(hits, helpHit{index: index, cost: cost})
	}
}

// A terminal picker for editing settings is different from a read-only list.
func describesEditor(detail string) bool {
	if searchWord(detail, "terminal") != 0 && searchWord(detail, "interactive") != 0 {
		return false
	}
	for _, noun := range []string{"form", "editor", "composer", "workbench", "studio"} {
		if searchWord(detail, noun) == 0 {
			return true
		}
	}
	return searchWord(detail, "picker") == 0 && searchWord(detail, "settings") == 0
}

func (index *searchIndex) matchHelp(words []string) []searchMatch {
	// Keep flags, shell syntax, and literal history searches on their existing paths.
	if len(words) == 0 {
		return nil
	}
	for _, word := range words {
		if strings.IndexFunc(word, func(r rune) bool { return !searchWordRune(r) }) >= 0 {
			return nil
		}
	}
	if index.help == nil {
		index.help = newHelpIndex(index.candidates, index.entries)
	}
	if matches := index.help.matches(words, index.entries, false); len(matches) > 0 {
		return matches
	}
	// Long option descriptions are the least specific evidence. Parse them
	// only for a miss and keep the tiers separate so typing order cannot change results.
	// A single word buried in an option description is a poor explanation
	// for suggesting the command. Require a second term and a known subject.
	searchOptions := len(words) >= 2 && slices.ContainsFunc(words, func(word string) bool { return len(index.help.postings[word]) > 0 })
	if searchOptions {
		if index.optionHelp == nil {
			index.optionHelp = index.help.withOptionHelp(index.candidates)
		}
		if matches := index.optionHelp.matches(words, index.entries, false); len(matches) > 0 {
			return matches
		}
	}
	// Only widen an exact final word after both existing tiers miss. An
	// unrelated "compose" route must not hide "voice composer" during typing.
	if matches := index.help.retryFinalPrefix(words, index.entries); len(matches) > 0 || !searchOptions {
		return matches
	}
	return index.optionHelp.retryFinalPrefix(words, index.entries)
}

func (h *helpIndex) retryFinalPrefix(words []string, entries []searchEntry) []searchMatch {
	last := words[len(words)-1]
	if len(last) < 3 {
		return nil
	}
	first, found := slices.BinarySearch(h.terms, last)
	if !found || first+1 == len(h.terms) || !strings.HasPrefix(h.terms[first+1], last) {
		return nil
	}
	return h.matches(words, entries, true)
}

func (h *helpIndex) matches(words []string, entries []searchEntry, widenFinal bool) []searchMatch {
	clear(h.scores)
	corrected := false
	for i, word := range words {
		first, found := slices.BinarySearch(h.terms, word)
		switch {
		case found && !(widenFinal && i == len(words)-1):
			h.scoreTerm(i, word)
		case i == len(words)-1 && len(word) >= 3 && first < len(h.terms) && strings.HasPrefix(h.terms[first], word):
			for _, term := range h.terms[first:] {
				if !strings.HasPrefix(term, word) {
					break
				}
				h.scoreTerm(i, term)
			}
		case !corrected && len(words) >= 2 && len(word) >= 5 && asciiLetters(word):
			// Correct a single unambiguous spelling, with other query words
			// still required. Never guess between two operations.
			var correction string
			for _, term := range h.terms {
				if WithinOneEdit(term, word) {
					if correction != "" {
						return nil
					}
					correction = term
				}
			}
			if correction == "" {
				return nil
			}
			h.scoreTerm(i, correction)
			corrected = true
		default:
			return nil
		}
	}
	var matches []searchMatch
	for i, score := range h.scores {
		if score.words == len(words) && (score.anchor || len(words) == 1) {
			entry := &entries[i]
			matches = append(matches, searchMatch{index: i, rank: [6]int{score.total, len(entry.routeWords), entry.length}})
		}
	}
	return matches
}

func (h *helpIndex) scoreTerm(position int, word string) {
	h.scoreWord(position, h.postings[word])
	if h.basePostings != nil {
		h.scoreWord(position, h.basePostings[word])
	}
}

// For a prefix, count a candidate once and keep its best matching field.
func (h *helpIndex) scoreWord(position int, hits []helpHit) {
	for _, hit := range hits {
		score := &h.scores[hit.index]
		if hit.cost <= optionWord {
			score.anchor = true
		}
		switch score.words {
		case position:
			score.words++
			score.total += hit.cost
			score.lastCost = hit.cost
		case position + 1:
			if hit.cost < score.lastCost {
				score.total += hit.cost - score.lastCost
				score.lastCost = hit.cost
			}
		}
	}
}
