//go:build !windows

package custom

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// This exercises Files' invocation capture and receipt writer without a PTY.
// The action substitutes only a successful synthetic API result.
func fileReceiptForExecutable(t *testing.T, executable, shell string) string {
	t.Helper()
	previous := os.Args
	os.Args = []string{executable}
	t.Cleanup(func() { os.Args = previous })
	var receipt bytes.Buffer
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "files", Commands: []*cli.Command{
		{Name: "upload", Flags: []cli.Flag{&cli.StringFlag{Name: "file"}, &cli.StringFlag{Name: "purpose"}}, Action: func(ctx context.Context, _ *cli.Command) error {
			invocation, _ := ctx.Value(fileInvocationKey{}).(fileInvocation)
			return writeFileReceipt(&receipt, gjson.Parse(fileReceiptFixture), shell, invocation)
		}},
	}}}}
	_, _, err := clihelp.Configure(root, os.Args)
	require.NoError(t, err)
	configureFileCommands(root)
	require.NoError(t, root.Run(t.Context(), []string{executable, "files", "upload", "--file", "synthetic.txt", "--purpose", "user_data"}))
	_, command, found := strings.Cut(receipt.String(), "\nDownload it: ")
	require.True(t, found, receipt.String())
	return strings.TrimSuffix(command, "\n")
}

func TestFileReceiptExecutableIdentity(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		binary, err := exec.LookPath(shell)
		if err != nil {
			t.Run(shell, func(t *testing.T) { t.Skipf("%s is unavailable", shell) })
			continue
		}
		for _, mode := range []string{"spaces", "apostrophe", "go-run-prefix", "control-directory"} {
			t.Run(shell+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				bin := filepath.Join(dir, "bin")
				require.NoError(t, os.Mkdir(bin, 0o700))
				t.Setenv("PATH", bin+":/usr/bin:/bin")
				for _, wrong := range []string{"openai", "go"} {
					require.NoError(t, os.WriteFile(filepath.Join(bin, wrong), []byte("#!/bin/sh\nprintf WRONG\n"), 0o700))
				}
				executable := filepath.Join(dir, "CLI space", "openai")
				switch mode {
				case "apostrophe":
					executable = filepath.Join(dir, "CLI's files", "openai")
				case "go-run-prefix":
					executable = "go run synthetic tool"
				case "control-directory":
					executable = filepath.Join(dir, "CLI\nfiles", "openai")
				}
				path := executable
				if !filepath.IsAbs(path) {
					path = filepath.Join(bin, path)
				}
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nprintf CORRECT\n"), 0o700))
				command := fileReceiptForExecutable(t, executable, shell)
				args := []string{"--noprofile", "--norc", "-c", command}
				switch shell {
				case "zsh":
					args = []string{"-f", "-c", command}
				case "fish":
					args = []string{"--no-config", "-c", command}
				case "pwsh":
					args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				process := exec.CommandContext(ctx, binary, args...)
				process.Dir = dir
				out, err := process.CombinedOutput()
				require.NoError(t, err, string(out))
				t.Logf("copied command=%q output=%q", command, out)
				require.Equal(t, "CORRECT", string(out), "copied receipt selected another executable")
			})
		}
	}
}
