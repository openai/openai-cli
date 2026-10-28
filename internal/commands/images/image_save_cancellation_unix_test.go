//go:build !windows

package images

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImageSavingCancellationOwnership(t *testing.T) {
	for _, mode := range []string{"API output", "picker workspace", "parent cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			plan := &imageOutputPlan{}
			stageSent := false
			switch mode {
			case "API output":
				plan = nil
			case "picker workspace":
				workspace := &imagePickerWorkspace{send: func(tea.Msg) { stageSent = true }}
				ctx = context.WithValue(ctx, imagePickerWorkspaceKey{}, workspace)
			case "parent cancellation":
				cancel()
			}
			command := &cli.Command{Name: "generate", Writer: io.Discard}
			sentinel := errors.New("original failure")
			if mode == "parent cancellation" {
				sentinel = context.Canceled
			}
			err := runWithImageSaving(ctx, command, plan, func(got context.Context, gotCommand *cli.Command) error {
				require.Same(t, command, gotCommand)
				if mode != "parent cancellation" {
					require.Same(t, ctx, got)
				}
				if mode == "picker workspace" {
					plan.setLoadingStage(0)
					require.True(t, stageSent)
				}
				return sentinel
			}, SavingOptions{})
			require.Same(t, sentinel, err)
		})
	}
}

func TestImageSavingCancellationSignalStatus(t *testing.T) {
	for _, test := range []struct {
		mode string
		code int
	}{
		{"nil", 130}, {"canceled", 130}, {"exit status", 7}, {"ordinary failure", 1}, {"blocked cleanup", -1},
	} {
		t.Run(test.mode, func(t *testing.T) {
			binary, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "-test.run=^TestImageSavingCancellationProcess$")
			child.Env = []string{"OPENAI_IMAGE_CANCELLATION_PROCESS=" + test.mode, "GOMAXPROCS=2", "HOME=" + t.TempDir()}
			stdout, err := child.StdoutPipe()
			require.NoError(t, err)
			require.NoError(t, child.Start())
			waited := false
			defer func() {
				cancel()
				if !waited {
					child.Wait()
				}
			}()
			reader := bufio.NewReader(stdout)
			line, err := reader.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "ready\n", line)
			require.NoError(t, child.Process.Signal(os.Interrupt))
			if test.mode == "blocked cleanup" {
				line, err = reader.ReadString('\n')
				require.NoError(t, err)
				require.Equal(t, "canceled\n", line)
				// Signal restoration runs in context.AfterFunc's goroutine.
				time.Sleep(100 * time.Millisecond)
				require.NoError(t, child.Process.Signal(os.Interrupt))
			}
			err = child.Wait()
			waited = true
			require.NoError(t, ctx.Err())
			require.Error(t, err)
			require.Equal(t, test.code, child.ProcessState.ExitCode())
			if test.mode == "blocked cleanup" {
				status := child.ProcessState.Sys().(syscall.WaitStatus)
				require.True(t, status.Signaled())
				require.Equal(t, syscall.SIGINT, status.Signal())
			}
		})
	}
}

// This fixture isolates real signals from the parent test process.
func TestImageSavingCancellationProcess(t *testing.T) {
	mode := os.Getenv("OPENAI_IMAGE_CANCELLATION_PROCESS")
	if mode == "" {
		return
	}
	command := &cli.Command{Name: "generate", Writer: io.Discard}
	err := runWithImageSaving(context.Background(), command, &imageOutputPlan{}, func(ctx context.Context, _ *cli.Command) error {
		fmt.Fprintln(os.Stdout, "ready")
		<-ctx.Done()
		switch mode {
		case "nil":
			return nil
		case "canceled":
			return context.Canceled
		case "exit status":
			return cli.Exit(context.Canceled, 7)
		case "ordinary failure":
			return errors.New("original failure")
		case "blocked cleanup":
			fmt.Fprintln(os.Stdout, "canceled")
			select {}
		}
		return errors.New("unknown fixture")
	}, SavingOptions{})
	var exit cli.ExitCoder
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
