package commandsuggest

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode"

	"github.com/urfave/cli/v3"
)

// Candidate is a declared command, never an instruction to execute it.
type Candidate struct {
	Command     string
	Description string
	// Only installed command routes have a path. Examples and shell history carry display text alone.
	path        string
	taskPhrases []TaskPhrase
}

// TaskPhrase describes a search for an installed command. Keep the default
// word order when reversing the words could change the requested operation.
type TaskPhrase struct {
	Text         string
	AnyWordOrder bool
}

// Catalog derives discovery from the current command tree on every invocation.
// The callbacks reuse help's ordering and authored examples without rendering help.
func Catalog(root *cli.Command, invocation string, taskPhrases map[string][]TaskPhrase, children func(*cli.Command) []*cli.Command, examples func(*cli.Command, string) []Candidate) []Candidate {
	var result []Candidate
	seen := map[string]int{}
	add := func(candidate Candidate) {
		if !validCommand(candidate.Command) {
			return
		}
		if index, ok := seen[candidate.Command]; ok {
			// An earlier help example can also be the installed command itself.
			if candidate.path != "" {
				result[index].path = candidate.path
				result[index].taskPhrases = candidate.taskPhrases
			}
			return
		}
		seen[candidate.Command] = len(result)
		candidate.Description = cleanDescription(candidate.Description)
		result = append(result, candidate)
	}
	var walk func(*cli.Command, string)
	walk = func(command *cli.Command, path string) {
		for _, candidate := range examples(command, path) {
			add(candidate)
		}
		for _, child := range children(command) {
			if child.Hidden || !safeCatalogName(child.Name) {
				continue
			}
			childPath := strings.TrimSpace(path + " " + child.Name)
			target := invocation + " " + childPath
			if len(children(child)) > 0 {
				target += " --help"
			}
			// Retired endpoints remain in help, but discovery should offer their
			// authored replacement examples instead of unavailable operations.
			if child.Metadata["help-command-section"] != "Retired commands" {
				add(Candidate{Command: target, Description: child.Usage, path: childPath, taskPhrases: taskPhrases[childPath]})
			}
			walk(child, childPath)
		}
	}
	if root != nil && validCommand(invocation) {
		walk(root, "")
	}
	return result
}

func safeCatalogName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_-.:@", r) {
			return false
		}
	}
	return true
}

func cleanDescription(text string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, text)), " ")
}

// ReadCandidates accepts the bounded, single-line suggestion transport only.
// Reject the whole input on malformed data; never silently select from a partial list.
func ReadCandidates(input io.Reader) ([]Candidate, error) {
	limited := &io.LimitedReader{R: input, N: maxCaptureBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxCommandBytes+2)
	scanner.Split(func(data []byte, eof bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i], nil
		}
		if eof && len(data) > 0 {
			return 0, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, nil
	})
	var result []Candidate
	seen := map[string]bool{}
	count := 0
	for scanner.Scan() {
		command := scanner.Text()
		count++
		if count > maxCommands || !validCommand(command) {
			return nil, errors.New("invalid command suggestions")
		}
		if !seen[command] {
			result = append(result, Candidate{Command: command})
			seen[command] = true
		}
	}
	if scanner.Err() != nil || limited.N == 0 {
		return nil, errors.New("invalid command suggestions")
	}
	if len(result) == 0 {
		return nil, errors.New("no recent command suggestions")
	}
	return result, nil
}
