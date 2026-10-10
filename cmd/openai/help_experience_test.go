package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/pkg/cmd"
)

func TestMainHelpExperienceRootMapAndOrdering(t *testing.T) {
	for _, args := range [][]string{{"openai", "--help"}, {"openai", "help", "--all"}} {
		got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=invalid"}, args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("offline overview failed: %+v", got)
		}
		last := -1
		for _, group := range clihelp.VisibleCommands(cmd.Command) {
			if group.Category != "API RESOURCE" {
				continue
			}
			index, occurrences := -1, 0
			for lineIndex, line := range strings.Split(got.stdout, "\n") {
				if fields := strings.Fields(line); len(fields) > 0 && fields[0] == group.Name {
					index = lineIndex
					occurrences++
				}
			}
			if index < 0 || index <= last || occurrences != 1 {
				t.Errorf("group %q missing, repeated, or out of order: %s", group.Name, got.stdout)
			}
			last = index
			if group.Usage == "" || !strings.Contains(strings.Join(strings.Fields(got.stdout), " "), group.Usage) {
				t.Errorf("group %q lacks its description: %s", group.Name, got.stdout)
			}
		}
		if strings.Contains(got.stdout, "admin:organization") || strings.Contains(got.stdout, "API RESOURCE:") {
			t.Errorf("overview exposed legacy discovery: %s", got.stdout)
		}
	}
}

func TestMainHelpExperienceUnknownTopicUsesParent(t *testing.T) {
	const suggestion = "Unknown help topic. Did you mean: openai help admin organization projects?"
	for _, args := range [][]string{
		{"openai", "help", "admin", "organization", "projcts"},
		{"openai", "help", "--all", "admin", "organization", "projcts"},
		{"openai", "admin", "organization", "help", "projcts"},
		{"openai", "--debug", "help", "admin", "organization", "projcts"},
	} {
		got := runMainDispatch(t, "bash", args...)
		if got.code != 3 || got.stdout != "" || !strings.Contains(got.stderr, suggestion) {
			t.Errorf("help lost parent suggestion or status: %q: %+v", args, got)
		}
	}
	got := runMainDispatch(t, "bash", "openai", "admin", "organization", "projcts")
	if got.code != 3 || got.stdout != "" || !strings.Contains(got.stderr, "Unknown command. Did you mean: openai admin organization projects?") {
		t.Errorf("direct typo lost its command suggestion: %+v", got)
	}
	got = runMainDispatch(t, "bash", "openai", "help", "admin", "organization", "zzzzzzzzzz")
	if got.code != 3 || got.stdout != "" || !strings.Contains(got.stderr, "openai help admin organization") || strings.Contains(got.stderr, "zzzzzzzzzz") {
		t.Errorf("unknown topic lacks contextual, value-safe recovery: %+v", got)
	}
	got = runMainDispatch(t, "bash", "openai", "--format-error", "json", "help", "admin", "organization", "projcts")
	var value map[string]any
	if got.code != 3 || got.stdout != "" || json.Unmarshal([]byte(got.stderr), &value) != nil {
		t.Errorf("structured help failure changed status or format: %+v", got)
	}
	if message, _ := value["message"].(string); !strings.Contains(message, suggestion) {
		t.Errorf("structured error lost contextual suggestion: %#v", value)
	}
}

func TestMainHelpExperienceCompleteMixedGroups(t *testing.T) {
	for _, path := range [][]string{{"admin", "organization"}, {"admin", "organization", "projects"}, {"beta", "threads", "runs"}} {
		group := cmd.Command
		for _, part := range path {
			group = group.Command(part)
		}
		for _, full := range []bool{false, true} {
			args := []string{"openai", "help"}
			if full {
				args = append(args, "--all")
			}
			got := runMainDispatch(t, "bash", append(args, path...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("group help failed: %+v", got)
			}
			for _, child := range clihelp.VisibleCommands(group) {
				if !strings.Contains(got.stdout, "  "+child.Name) {
					t.Errorf("%v full=%v lost child %q: %s", path, full, child.Name, got.stdout)
				}
			}
			if strings.Contains(got.stdout, "more in full help") {
				t.Errorf("immediate groups remain hidden: %s", got.stdout)
			}
			if group.Command("create") != nil && !strings.Contains(got.stdout, "ACTIONS") {
				t.Errorf("mixed group lacks Actions: %s", got.stdout)
			}
		}
	}
}

func TestMainHelpExperienceLegacyTypoSuggestions(t *testing.T) {
	for _, topic := range [][]string{{"audio:transcriptins"}, {"help", "audio:transcriptins"}} {
		suggestion := "Unknown command. Did you mean: openai audio:transcriptions?"
		if topic[0] == "help" {
			suggestion = "Unknown help topic. Did you mean: openai help audio:transcriptions?"
		}
		for _, format := range []string{"text", "json"} {
			args := append([]string{"openai", "--format-error", format}, topic...)
			got := runMainDispatch(t, "bash", args...)
			message := got.stderr
			if format == "json" {
				var value map[string]any
				if err := json.Unmarshal([]byte(got.stderr), &value); err != nil {
					t.Fatalf("legacy typo error is not JSON: %v: %+v", err, got)
				}
				message, _ = value["message"].(string)
			}
			if got.code != 3 || got.stdout != "" || !strings.Contains(message, suggestion) {
				t.Errorf("legacy typo lost its compatible suggestion: %q: %+v", args, got)
			}
		}
	}
}
