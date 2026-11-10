package main

import (
	"strings"
	"testing"
)

func TestMainAudioLocalHelpScopesOutput(t *testing.T) {
	for _, tc := range []struct{ name, option string }{
		{"licenses", "--transform licenses --raw-output"},
		{"history", "--format json"},
		{"inspect", "--format json"},
		{"play", "--format-error json"},
		{"export", "--quiet"},
	} {
		for _, full := range []bool{false, true} {
			args := []string{"openai", "audio", tc.name, "--help"}
			if full {
				args = []string{"openai", "help", "--all", "audio", tc.name}
			}
			got := runMainDispatch(t, "zsh", args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, tc.option) ||
				strings.Contains(got.stdout, "Key setup:") || strings.Contains(got.stdout, "--api-key") != full {
				t.Errorf("%v: unexpected local help: %+v", args, got)
			}
			wantDescription := "Uses local audio history without API requests."
			if tc.name == "licenses" {
				wantDescription = "Prints the embedded dependency notice"
			}
			if !strings.Contains(got.stdout, wantDescription) {
				t.Errorf("%v: local description missing", args)
			}
		}
	}
}
