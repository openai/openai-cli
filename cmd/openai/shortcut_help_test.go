package main

import (
	"strings"
	"testing"
)

func TestMainShortcutHelpPreservesPurpose(t *testing.T) {
	for _, tc := range []struct {
		name, purpose string
	}{
		{"transcribe", "Convert audio to text"},
		{"translate", "Translate audio to English text"},
		{"speak", "Generate speech from text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, args := range [][]string{
				{"openai", "--help"},
				{"openai", "help", "--all"},
				{"openai", tc.name, "--help"},
				{"openai", "help", "--all", tc.name},
			} {
				got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=invalid"}, args...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("offline help failed for %v: %+v", args, got)
				}
				text := strings.Join(strings.Fields(got.stdout), " ")
				for _, want := range []string{tc.purpose, "shortcut for audio " + tc.name} {
					if !strings.Contains(text, want) {
						t.Errorf("%v hides %q: %s", args, want, got.stdout)
					}
				}
			}
		})
	}
}
