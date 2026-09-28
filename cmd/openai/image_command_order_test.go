package main

import (
	"slices"
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
			var names []string
			inCommands := false
			for _, line := range strings.Split(result.stdout, "\n") {
				if strings.TrimRight(line, ":") == "COMMANDS" {
					inCommands = true
					continue
				}
				if !inCommands {
					continue
				}
				if line != "" && line[0] != ' ' && line[0] != '\t' {
					break
				}
				fields := strings.Fields(line)
				if len(fields) > 0 && slices.Contains(want, fields[0]) {
					names = append(names, fields[0])
				}
			}
			require.Equal(t, want, names, result.stdout)
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
			for _, line := range strings.Split(result.stdout, "\n") {
				// zsh and fish append descriptions, including existing newlines.
				name, _, _ := strings.Cut(line, ":")
				name, _, _ = strings.Cut(name, "\t")
				if slices.Contains(want, name) {
					names = append(names, name)
				}
			}
			require.Equal(t, want, names, result.stdout)
		})
	}
}
