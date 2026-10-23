package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainImageCommandHelpOrder(t *testing.T) {
	want := []string{"generate", "edit", "preview", "models", "inline", "create-variation"}
	for _, args := range [][]string{
		{"images"}, {"images", "--help"}, {"images", "-h"}, {"images", "--h"},
		{"help", "images"}, {"help", "--all", "images"}, {"images", "help", "--all"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			result := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
			require.Zero(t, result.code, result.stderr)
			require.Empty(t, result.stderr)
			names := fullHelpCommandNames(result.stdout, map[string]bool{"ACTIONS": true, "SETTINGS": true, "RETIRED COMMANDS": true})
			require.Equal(t, want, names, result.stdout)
			require.Less(t, strings.Index(result.stdout, "ACTIONS\n"), strings.Index(result.stdout, "SETTINGS\n"))
			require.Less(t, strings.Index(result.stdout, "SETTINGS\n"), strings.Index(result.stdout, "RETIRED COMMANDS\n"))
		})
	}
}

func TestMainImageCommandCompletionOrder(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(style, func(t *testing.T) {
			prefix := []string{"openai", "__complete"}
			if style == "bash" || style == "fish" {
				prefix = append(prefix, "--")
			} else if style == "pwsh" {
				prefix = append(prefix, "openai")
			}
			result := runMainDispatch(t, style, append(prefix, "images", "")...)
			require.Zero(t, result.code, result.stderr)
			require.Empty(t, result.stderr)
			want := []string{"generate", "edit", "preview", "models", "inline", "create-variation"}
			var names []string
			for _, line := range strings.Split(strings.TrimSuffix(result.stdout, "\n"), "\n") {
				name := line
				if style == "zsh" {
					name, _, _ = strings.Cut(line, ":")
				} else if style == "fish" {
					name, _, _ = strings.Cut(line, "\t")
				}
				names = append(names, name)
			}
			require.Equal(t, want, names, result.stdout)
		})
	}
}
