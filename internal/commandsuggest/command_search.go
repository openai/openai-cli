package commandsuggest

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Search returns declared candidates in the same order as the interactive picker.
func Search(candidates []Candidate, query string) []Candidate {
	indices := newSearchIndex(candidates).search(query)
	matches := make([]Candidate, 0, len(indices))
	for _, index := range indices {
		matches = append(matches, candidates[index])
	}
	return matches
}

// Keep parsed fields for the lifetime of the picker. No catalog work runs when
// the user moves the cursor or selects a result.
type searchIndex struct {
	candidates []Candidate
	entries    []searchEntry
	browse     []int
	catalog    bool
	help       *helpIndex
	optionHelp *helpIndex
	verbs      map[string]bool
}

type searchEntry struct {
	path, arguments, prose string
	routeWords             []string
	phrases                []indexedTaskPhrase
	length                 int
	resourceWords          int
	direction              string
}

func newSearchIndex(candidates []Candidate) *searchIndex {
	index := &searchIndex{candidates: candidates, entries: make([]searchEntry, len(candidates))}
	for i, candidate := range candidates {
		if candidate.path != "" {
			index.catalog = true
		}
		command := candidate.Command
		if candidate.commandText != "" {
			// A directory or quoted executable name describes the local machine,
			// not the operation. Keep the conventional "openai ..." search form.
			command = "openai " + candidate.commandText
		}
		command = strings.ToLower(command)
		path, arguments, hasFlags := strings.Cut(command, " -")
		if hasFlags {
			arguments = "-" + arguments
		}
		index.entries[i] = searchEntry{
			path: path, arguments: arguments, prose: searchableProse(candidate.Description),
			routeWords: strings.FieldsFunc(candidate.path, func(r rune) bool { return !searchWordRune(r) }),
			length:     utf8.RuneCountInString(command),
		}
		if cmd := candidate.definition; cmd != nil {
			if index.verbs == nil {
				index.verbs = make(map[string]bool)
			}
			index.verbs[cmd.Name] = true
			for _, alias := range cmd.Aliases {
				index.verbs[alias] = true
			}
			entry := &index.entries[i]
			leafWords := len(strings.FieldsFunc(cmd.Name, func(r rune) bool { return !searchWordRune(r) }))
			entry.resourceWords = len(entry.routeWords) - leafWords
			// Read only the first claim in the command's own summary.
			summary := cmd.Usage
			if end := strings.IndexAny(summary, ".;\n"); end >= 0 {
				summary = summary[:end]
			}
			entry.direction = conversionPhrase(strings.ToLower(summary))
		}
		for _, phrase := range candidate.taskPhrases {
			index.entries[i].phrases = append(index.entries[i].phrases, phrase.indexed())
		}
	}
	return index
}

func searchableProse(text string) string {
	description := strings.Fields(strings.ToLower(text))
	for i, word := range description {
		if label, _, link := strings.Cut(word, "]("); link {
			description[i] = label
		} else if strings.Contains(word, "://") || strings.Contains(word, "www.") {
			description[i] = ""
		}
	}
	return strings.Join(description, " ")
}

type searchMatch struct {
	index int
	task  taskMatch
	// Prefer command names, arguments, then descriptions. Within each tier,
	// consider complete words, canonical commands, phrases and command length.
	rank [6]int
}

func (index *searchIndex) search(query string) []int {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		if index.browse == nil {
			index.browse = browseOrder(index.candidates)
		}
		return index.browse
	}
	query = strings.Join(words, " ")
	direction := queryDirection(query)
	matches := index.literalMatches(query, words, direction)
	if len(matches) == 0 && index.catalog {
		if natural := taskQuery(query); natural != query && natural != "" {
			query = natural
			words = strings.Fields(query)
			direction = queryDirection(query)
			matches = index.literalMatches(query, words, direction)
		}
	}
	if len(matches) == 0 && index.catalog {
		words = strings.Fields(strings.TrimPrefix(query, "openai "))
		for i := range index.entries {
			entry := &index.entries[i]
			if edits := entry.recoverRoute(words); edits >= 0 {
				matches = append(matches, searchMatch{index: i, rank: [6]int{edits, len(entry.routeWords), entry.length}})
			}
		}
	}
	if len(matches) == 0 && index.catalog {
		matches = index.matchHelp(words)
	}
	if from, to, ok := strings.Cut(direction, " to "); ok && from != to {
		reverse := to + " to " + from
		matches = slices.DeleteFunc(matches, func(m searchMatch) bool {
			return m.task < exactTask && index.entries[m.index].direction == reverse
		})
	}
	slices.SortStableFunc(matches, func(a, b searchMatch) int {
		if order := cmp.Compare(b.task, a.task); order != 0 {
			return order
		}
		return slices.Compare(a.rank[:], b.rank[:])
	})
	if len(matches) == 0 {
		return nil
	}
	result := make([]int, len(matches))
	for i, match := range matches {
		result[i] = match.index
	}
	return result
}

// Read the same directional wording from a request and from an installed
// command's usage. Keep input and output separate when the names overlap.
func conversionPhrase(text string) string {
	verb, rest, ok := strings.Cut(text, " ")
	if !ok {
		return ""
	}
	var from, to string
	switch verb {
	case "convert", "converts", "turn", "transform", "transforms", "translate", "translates", "change":
		from, to, ok = strings.Cut(rest, " into ")
		if !ok {
			from, to, ok = strings.Cut(rest, " to ")
		}
	case "generate", "generates", "produce", "produces":
		to, from, ok = strings.Cut(rest, " from ")
	default:
		return ""
	}
	if !ok || from == "" || to == "" {
		return ""
	}
	return resourceNoun(from, "") + " to " + resourceNoun(to, "")
}

func queryDirection(query string) string {
	if direction := conversionPhrase(query); direction != "" {
		return direction
	}
	if strings.Contains(query, " to ") {
		return query
	}
	return ""
}

func (index *searchIndex) literalMatches(query string, words []string, direction string) []searchMatch {
	var matches []searchMatch
	route := strings.TrimPrefix(query, "openai ")
	request := parseResourceRequest(route, index.verbs)
	for i := range index.entries {
		entry := &index.entries[i]
		candidate := &index.candidates[i]
		match := searchMatch{index: i}
		if candidate.path != "" && candidate.path == route {
			match.task = literalRoute
		} else if len(words) > 1 {
			for _, phrase := range entry.phrases {
				match.task = max(match.task, phrase.match(query, words))
				if direction != "" && !phrase.anyOrder && phrase.text == direction {
					match.task = exactTask
				}
			}
		}
		if match.task == noTaskMatch && direction != "" && entry.direction == direction {
			match.task = resourceTask
			// Give an equally documented command with the resource in its route priority.
			entry.rankWords(query, words, &match.rank)
		}
		if match.task == noTaskMatch && len(request.resource) > 0 && request.match(candidate, entry, &match.rank) {
			match.task = resourceTask
		}
		if match.task != noTaskMatch || entry.rankWords(query, words, &match.rank) {
			matches = append(matches, match)
		}
	}
	return matches
}

func (entry *searchEntry) rankWords(query string, words []string, rank *[6]int) bool {
	for i, word := range words {
		partial := searchWord(entry.path, word)
		if partial != 0 && !slices.Contains(words[:i], word) && slices.ContainsFunc(entry.routeWords, func(term string) bool { return pluralPair(term, word) }) {
			partial = 0
		}
		if partial < 0 {
			partial = searchWord(entry.arguments, word)
			if partial >= 0 {
				rank[1]++
			} else {
				partial = searchWord(entry.prose, word)
				if partial == 1 && utf8.RuneCountInString(word) < 3 {
					return false
				}
				rank[0]++
			}
		}
		if partial < 0 {
			return false
		}
		rank[2] += partial
	}
	if rank[0] == 0 {
		if len(entry.routeWords) == 0 {
			rank[3] = 1 // An authored example yields to an equally good installed command.
		}
		rank[5] = entry.length
	}
	if entry.path != query && !strings.HasSuffix(entry.path, " "+query) {
		rank[4] = 1
		if searchWord(entry.path, query) < 0 {
			rank[4] = 2
		}
	}
	return true
}

func pluralPair(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(a) < 3 {
		return false
	}
	switch {
	case len(b) == len(a)+1 && strings.TrimSuffix(b, "s") == a:
		return !strings.HasSuffix(b, "us") && !strings.HasSuffix(b, "ss")
	case len(b) == len(a)+2 && strings.TrimSuffix(b, "es") == a:
		return strings.HasSuffix(a, "ch") || strings.HasSuffix(a, "sh") || strings.HasSuffix(a, "s") || strings.HasSuffix(a, "x")
	case strings.HasSuffix(a, "y") && strings.HasSuffix(b, "ies"):
		return a[:len(a)-1] == b[:len(b)-3]
	}
	return false
}

// Relax conversational wording only when the literal search found nothing.
// Retain directions such as "to" and never reinterpret flags or shell syntax.
func taskQuery(query string) string {
	for _, r := range query {
		if !searchWordRune(r) && r != ' ' && r != '-' && r != '?' {
			return query
		}
	}
	if strings.HasPrefix(query, "-") || strings.Contains(query, " -") {
		return query
	}
	query = strings.TrimSuffix(query, "?")
	for _, prefix := range []string{"how do i ", "how can i ", "i want to ", "please "} {
		query = strings.TrimPrefix(query, prefix)
	}
	words := strings.Fields(strings.ReplaceAll(query, "-", " "))
	list := slices.Contains(words, "list") || slices.Contains(words, "ls")
	words = slices.DeleteFunc(words, func(word string) bool {
		return (list && word == "all") || word == "my" || article(word)
	})
	return strings.Join(words, " ")
}

func article(word string) bool {
	return word == "the" || word == "a" || word == "an"
}

// Recover the common ls abbreviation or one misspelled route word. Use
// installed route words only. Return the edit count, or -1 for no match.
func (entry *searchEntry) recoverRoute(words []string) int {
	if len(words) > len(entry.routeWords) {
		return -1
	}
	edits := 0
	for _, word := range words {
		if word == "ls" && slices.Contains(entry.routeWords, "list") {
			continue
		}
		strict := false
		near := false
		for _, term := range entry.routeWords {
			if strings.HasPrefix(term, word) || pluralPair(term, word) {
				strict = true
				break
			}
			if edits == 0 && len(word) >= 4 && len(term) >= 4 && WithinOneEdit(term, word) && asciiLetters(word) {
				near = true
			}
		}
		if !strict {
			if !near {
				return -1
			}
			edits++
		}
	}
	return edits
}

func asciiLetters(s string) bool {
	for i := range len(s) {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// WithinOneEdit reports whether a and b differ by at most one insertion,
// deletion, substitution, or transposition of adjacent characters.
func WithinOneEdit(a, b string) bool {
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

type taskMatch int

const (
	noTaskMatch taskMatch = iota
	resourceTask
	reorderedTask
	orderedTaskPrefix
	exactTask
	literalRoute
)

type indexedTaskPhrase struct {
	text     string
	terms    []string
	anyOrder bool
}

func (p TaskPhrase) indexed() indexedTaskPhrase {
	terms := strings.Fields(strings.ToLower(p.Text))
	return indexedTaskPhrase{text: strings.Join(terms, " "), terms: terms, anyOrder: p.AnyWordOrder}
}

func (p indexedTaskPhrase) match(query string, words []string) taskMatch {
	if p.text == query {
		return exactTask
	}
	if strings.HasPrefix(p.text, query) {
		return orderedTaskPrefix
	}
	if !p.anyOrder || len(words) > len(p.terms) {
		return noTaskMatch
	}
	terms := slices.Clone(p.terms)
	contentWords := 0
	for i, word := range words {
		if !article(word) {
			contentWords++
		}
		// Consume a distinct term for each word. Only the last word can
		// still be incomplete while the user types in the picker.
		index := slices.IndexFunc(terms, func(term string) bool {
			return strings.TrimSuffix(term, "s") == strings.TrimSuffix(word, "s") ||
				(i == len(words)-1 && strings.HasPrefix(term, word))
		})
		if index < 0 {
			return noTaskMatch
		}
		terms = slices.Delete(terms, index, index+1)
	}
	if contentWords < 2 {
		return noTaskMatch
	}
	return reorderedTask
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
