package main

import (
	"strings"
	"testing"
)

func TestMainShortcutHelpPreservesPurpose(t *testing.T) {
	var shared []mainDispatchResult
	for _, args := range [][]string{{"openai", "--help"}, {"openai", "help", "--all"}} {
		shared = append(shared, runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=invalid"}, args...))
	}
	for _, tc := range []struct {
		name, purpose string
	}{
		{"transcribe", "Convert audio to text"},
		{"translate", "Translate audio to English text"},
		{"speak", "Generate speech from text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := append([]mainDispatchResult(nil), shared...)
			for _, args := range [][]string{
				{"openai", tc.name, "--help"},
				{"openai", "help", "--all", tc.name},
			} {
				results = append(results, runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=invalid"}, args...))
			}
			for _, got := range results {
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("offline help failed: %+v", got)
				}
				text := strings.Join(strings.Fields(got.stdout), " ")
				for _, want := range []string{tc.purpose, "shortcut for audio " + tc.name} {
					if !strings.Contains(text, want) {
						t.Errorf("help hides %q: %s", want, got.stdout)
					}
				}
			}
		})
	}
}
