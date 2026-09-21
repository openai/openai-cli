package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageInlineOfferSkipsInputAndSetupWhenIneligibleOrReady(t *testing.T) {
	for _, ready := range []bool{false, true} {
		for _, interactive := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "unconfigured", true: "ready"}[ready], map[bool]string{false: "noninteractive", true: "interactive"}[interactive]}, "/"), func(t *testing.T) {
				if interactive && !ready {
					t.Skip("confirmation path covered separately")
				}
				var output bytes.Buffer
				var checked, fallback int
				err := runImageInlineOffer(t.Context(), &output, interactive, imageInlineOfferServices{
					ready:    func(context.Context) (bool, error) { checked++; return ready, nil },
					confirm:  func(context.Context) (bool, error) { t.Fatal("read input"); return false, nil },
					setup:    func(context.Context) error { t.Fatal("changed profile"); return nil },
					fallback: func(context.Context) error { fallback++; return nil },
				})
				require.NoError(t, err)
				require.Empty(t, output.String())
				if interactive {
					require.Equal(t, 1, checked)
					require.Zero(t, fallback)
				} else {
					require.Zero(t, checked)
					require.Equal(t, 1, fallback)
				}
			})
		}
	}
}

func TestImageInlineOfferChoiceOnlyChangesCurrentTabAfterAcceptance(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "decline", true: "accept"}[accepted], func(t *testing.T) {
			var output bytes.Buffer
			var confirms, setups int
			err := runImageInlineOffer(t.Context(), &output, true, imageInlineOfferServices{
				ready:    func(context.Context) (bool, error) { return false, nil },
				confirm:  func(context.Context) (bool, error) { confirms++; return accepted, nil },
				setup:    func(context.Context) error { setups++; return nil },
				fallback: func(context.Context) error { t.Fatal("unexpected fallback"); return nil },
			})
			require.NoError(t, err)
			require.Equal(t, 1, confirms)
			require.Contains(t, output.String(), "THIS Apple Terminal tab")
			require.Contains(t, output.String(), "Keeps your text style, font size, profile and colors")
			require.Contains(t, output.String(), "[y/N]")
			if accepted {
				require.Equal(t, 1, setups)
			} else {
				require.Zero(t, setups)
				require.Contains(t, output.String(), imageInlineExecutable()+" images inline setup")
			}
		})
	}
}

func TestImageInlineOfferPropagatesFailuresBeforeAnySetup(t *testing.T) {
	sentinel := errors.New("synthetic failure")
	for _, stage := range []string{"fallback", "readiness", "prompt output", "choice", "setup", "cancel before", "cancel during readiness", "cancel during choice"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var output bytes.Buffer
			var out io.Writer = &output
			var confirms, setups int
			if stage == "cancel before" {
				cancel()
			}
			if stage == "prompt output" {
				out = imageInlineOfferErrorWriter{sentinel}
			}
			err := runImageInlineOffer(ctx, out, stage != "fallback", imageInlineOfferServices{
				ready: func(context.Context) (bool, error) {
					if stage == "readiness" {
						return false, sentinel
					}
					if stage == "cancel during readiness" {
						cancel()
					}
					return false, nil
				},
				confirm: func(context.Context) (bool, error) {
					confirms++
					if stage == "choice" {
						return false, sentinel
					}
					if stage == "cancel during choice" {
						cancel()
					}
					return true, nil
				},
				setup:    func(context.Context) error { setups++; return sentinel },
				fallback: func(context.Context) error { return sentinel },
			})
			if strings.HasPrefix(stage, "cancel") {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, sentinel)
			}
			if stage == "setup" {
				require.Equal(t, 1, setups)
			} else {
				require.Zero(t, setups)
			}
			if stage == "fallback" || stage == "readiness" || stage == "prompt output" || stage == "cancel before" || stage == "cancel during readiness" {
				require.Zero(t, confirms)
			}
		})
	}
}

type imageInlineOfferErrorWriter struct{ err error }

func (w imageInlineOfferErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestImageInlineOfferRequiresMatchingTerminalInput(t *testing.T) {
	for _, test := range []struct {
		input, output string
		want          bool
	}{
		{"/dev/ttys001", "/dev/ttys001", true},
		{"/dev/ttys001", "/dev/ttys002", false},
		{"", "/dev/ttys001", false},
		{"/dev/ttys001", "", false},
		{"", "", false},
	} {
		require.Equal(t, test.want, imageInlineOfferSameTTY(test.input, test.output))
	}
}

func TestImageInlineOfferNeverReadsRegularFileAsTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-input")
	require.NoError(t, os.WriteFile(path, []byte("yes\n"), 0600))
	input, err := os.Open(path)
	require.NoError(t, err)
	defer input.Close()
	accepted, err := confirmImageInlineTTY(t.Context(), path, input)
	require.ErrorContains(t, err, "terminal input changed")
	require.False(t, accepted)
	remaining, err := io.ReadAll(input)
	require.NoError(t, err)
	require.Equal(t, "yes\n", string(remaining))
}

// This only runs the fixed read/classify script with synthetic pipe input.
// It never opens a terminal, accesses a native profile, or calls an API.
func TestImageInlineOfferReadsOnlyExplicitYes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the production reader is local macOS only")
	}
	for _, test := range []struct {
		input string
		want  bool
	}{
		{"y\n", true}, {"YES\n", true}, {"  yes \n", true},
		{"\n", false}, {"n\n", false}, {"no\n", false}, {"\"yes\"\n", false},
		{"", false}, {"yes", false}, {"yes; exit 0\n", false},
	} {
		t.Run(strings.ReplaceAll(test.input, "\n", "newline"), func(t *testing.T) {
			input, writer, err := os.Pipe()
			require.NoError(t, err)
			defer input.Close()
			_, err = io.WriteString(writer, test.input)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			accepted, err := readImageInlineChoice(t.Context(), input)
			require.NoError(t, err)
			require.Equal(t, test.want, accepted)
		})
	}
}

func TestImageInlineOfferDoesNotEvaluateInputOrShellStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the production reader is local macOS only")
	}
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	startup := filepath.Join(t.TempDir(), "startup")
	require.NoError(t, os.WriteFile(startup, []byte("touch "+quoteImageShellArgument(marker)+"\n"), 0600))
	t.Setenv("ENV", startup)
	t.Setenv("BASH_ENV", startup)
	input, writer, err := os.Pipe()
	require.NoError(t, err)
	defer input.Close()
	_, err = io.WriteString(writer, "$(touch "+quoteImageShellArgument(marker)+")\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	accepted, err := readImageInlineChoice(t.Context(), input)
	require.NoError(t, err)
	require.False(t, accepted)
	_, err = os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestImageInlineOfferCancellationLeavesNoReader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the production reader is local macOS only")
	}
	input, writer, err := os.Pipe()
	require.NoError(t, err)
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	accepted, err := readImageInlineChoice(ctx, input)
	require.False(t, accepted)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = io.WriteString(writer, "next command\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	remaining, err := io.ReadAll(input)
	require.NoError(t, err)
	require.Equal(t, "next command\n", string(remaining))
}

func TestImageInlineOfferReadsExactlyOneLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the production reader is local macOS only")
	}
	input, writer, err := os.Pipe()
	require.NoError(t, err)
	defer input.Close()
	_, err = io.WriteString(writer, "yes\nnext command\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	accepted, err := readImageInlineChoice(t.Context(), input)
	require.NoError(t, err)
	require.True(t, accepted)
	remaining, err := io.ReadAll(input)
	require.NoError(t, err)
	require.Equal(t, "next command\n", string(remaining))
}
