//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMainAudioVoiceFIFOOrdering(t *testing.T) {
	for _, route := range [][]string{{"audio", "voices", "create"}, {"audio:voices", "create"}} {
		t.Run(strings.Join(route, " "), func(t *testing.T) {
			home, env, requests := localRecoveryEnvironment(t)
			fifo := filepath.Join(home, "synthetic-private-unwritten.fifo")
			require.NoError(t, unix.Mkfifo(fifo, 0600))
			typeFiles := map[string]string{}
			for _, fixture := range []struct{ name, contents string }{
				{"sample", "audio_sample"}, {"prompt", "prompt"}, {"invalid", "synthetic-private-invalid"},
			} {
				path := filepath.Join(home, "synthetic-private-"+fixture.name+".txt")
				require.NoError(t, os.WriteFile(path, []byte(fixture.contents), 0600))
				typeFiles[fixture.name] = "@" + path
			}
			for _, tc := range []struct {
				name, input, message string
				flags                []string
			}{
				{name: "default missing consent", flags: []string{"--name", "Synthetic"}, message: audioVoiceMissing(route, "--consent")},
				{name: "explicit missing consent", flags: []string{"--type", "audio_sample", "--name", "Synthetic"}, message: audioVoiceMissing(route, "--consent")},
				{name: "prompt missing fields", flags: []string{"--type", "prompt"}, message: audioVoiceMissing(route, "--name, --prompt")},
				{name: "invalid literal before missing fields", flags: []string{"--type", "synthetic-private-invalid"}, message: audioVoiceInvalidType},
				{name: "null before missing fields", input: `{"type":null}`, message: audioVoiceInvalidType},
				{name: "aggregate before nested references", input: audioVoiceJSON(t, map[string]any{"type": []any{"@" + fifo}}), message: audioVoiceInvalidType},
				{name: "file missing consent before FIFO", flags: []string{"--name", "Synthetic", "--type", typeFiles["sample"]}, message: audioVoiceMissing(route, "--consent")},
				{name: "stdin missing consent before FIFO", input: "audio_sample", flags: []string{"--name", "Synthetic", "--type", "@-"}, message: audioVoiceMissing(route, "--consent")},
				{name: "file missing prompt before FIFO", flags: []string{"--name", "Synthetic", "--type", typeFiles["prompt"]}, message: audioVoiceMissing(route, "--prompt")},
				{name: "stdin missing prompt before FIFO", input: "prompt", flags: []string{"--name", "Synthetic", "--type", "@-"}, message: audioVoiceMissing(route, "--prompt")},
				{name: "file invalid type before FIFO", flags: []string{"--name", "Synthetic", "--type", typeFiles["invalid"]}, message: audioVoiceInvalidType},
				{name: "stdin invalid type before FIFO", input: "synthetic-private-invalid", flags: []string{"--name", "Synthetic", "--type", "@-"}, message: audioVoiceInvalidType},
				{name: "missing type file before FIFO", flags: []string{"--name", "Synthetic", "--type", "@" + filepath.Join(home, "synthetic-private-missing.txt")}, message: "Could not open the file for --type. Check the path and permissions."},
			} {
				t.Run(tc.name, func(t *testing.T) {
					args := append([]string{"openai", "--format-error", "text"}, route...)
					args = append(args, "--audio-sample", fifo)
					args = append(args, tc.flags...)
					got := runMainDispatchWithStdin(t, "", env, shellFileInput(t, []byte(tc.input)), args...)
					require.Equal(t, tc.message, localRecoveryMessage(t, got, localRecoveryMode{}, 1))
					require.Zero(t, requests.Load())
				})
			}
		})
	}
}
