package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/output/listview"
)

func TestInteractiveListOutputScope(t *testing.T) {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		t.Skip("run the compiled test executable with terminal stdin and stdout")
	}
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	for operation, want := range map[string]bool{
		"(resource) files > (method) list":                       true,
		"(resource) batches > (method) list":                     true,
		"(resource) admin.organization.projects > (method) list": true,
		"(resource) vector_stores > (method) list":               false,
		"(resource) models > (method) list":                      false,
		"(resource) files > (method) retrieve":                   false,
		"unknown":                                                false,
		"":                                                       false,
	} {
		for _, format := range []string{"", "auto"} {
			t.Run(operation+"/"+format, func(t *testing.T) {
				if got := InteractiveListOutput(ShowJSONOpts{Operation: operation, OutputKind: OutputPageItem, Format: format}); got != want {
					t.Fatalf("got %v, want %v", got, want)
				}
			})
		}
	}
	for _, mode := range []string{"text", "json", "raw", "extraction", "raw-output", "CI", "dumb", "redirected", "resource"} {
		t.Run("bypass/"+mode, func(t *testing.T) {
			opts := ShowJSONOpts{Operation: "(resource) files > (method) list", OutputKind: OutputPageItem}
			switch mode {
			case "text", "json", "raw":
				opts.Format = mode
			case "extraction":
				opts.Transform = "id"
			case "raw-output":
				opts.RawOutput = true
			case "CI":
				t.Setenv("CI", "true")
			case "dumb":
				t.Setenv("TERM", "dumb")
			case "redirected":
				opts.Stdout = io.Discard
			case "resource":
				opts.OutputKind = OutputResponse
			}
			if InteractiveListOutput(opts) {
				t.Fatal("navigation intercepted an existing bypass")
			}
		})
	}
}

func TestListNavigationOutputFailureDiagnostic(t *testing.T) {
	outputFailure := &os.PathError{Op: "write", Path: "/synthetic-private/output", Err: io.ErrClosedPipe}
	pageFailure := &listview.PaginationError{}
	joinedFailure := errors.Join(pageFailure, context.Canceled)
	const outputMessage = "A local file could not be read or written. Check your file arguments and permissions."
	const timeoutMessage = "The request timed out. The API may have received it; check its status before repeating it."
	for _, tt := range []struct {
		name       string
		callerErr  error
		fetchError func(error) error
		modelError error
		runError   error
		wantCause  error
		want       string
	}{
		{name: "fetch canceled", want: outputMessage},
		{name: "fetch wrapped", fetchError: func(err error) error { return fmt.Errorf("request: %w", err) }, want: outputMessage},
		{name: "fetch finished", fetchError: func(error) error { return nil }, want: outputMessage},
		{name: "fetch joined", fetchError: func(err error) error {
			return fmt.Errorf("request: %w", errors.Join(pageFailure, err))
		}, wantCause: pageFailure, want: pageFailure.Error()},
		{name: "model joined", modelError: errors.Join(pageFailure, context.Canceled), wantCause: pageFailure, want: pageFailure.Error()},
		{name: "received fetch failure", modelError: joinedFailure, fetchError: func(error) error { return joinedFailure }, wantCause: pageFailure, want: pageFailure.Error()},
		{name: "program joined", runError: io.ErrUnexpectedEOF, wantCause: io.ErrUnexpectedEOF, want: outputMessage},
		{name: "request timeout", fetchError: func(error) error { return context.DeadlineExceeded }, wantCause: context.DeadlineExceeded, want: timeoutMessage},
		{name: "caller canceled", callerErr: context.Canceled, wantCause: context.Canceled, want: "Request canceled."},
		{name: "caller timeout", callerErr: context.DeadlineExceeded, wantCause: context.DeadlineExceeded, want: timeoutMessage},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				caller := context.Background()
				if tt.callerErr == context.Canceled {
					var cancel context.CancelFunc
					caller, cancel = context.WithCancel(caller)
					cancel()
				} else if tt.callerErr == context.DeadlineExceeded {
					var cancel context.CancelFunc
					caller, cancel = context.WithDeadline(caller, time.Unix(1, 0))
					defer cancel()
				}
				request, cancel := context.WithCancel(caller)
				defer cancel()
				program, stop := context.WithCancel(caller)
				defer stop()
				fetch := func() (listview.Page, error) {
					<-request.Done()
					err := request.Err()
					if tt.fetchError != nil {
						err = tt.fetchError(err)
					}
					return listview.Page{}, err
				}
				m := listview.New(listViewerOptions(ShowJSONOpts{Context: caller}), func() (listview.Page, error) { return fetch() }, cancel, 80, 24)
				if tt.modelError != nil {
					// Deliver the same model error through the event loop, then restore
					// the pending request used by the original shutdown contract.
					requestFetch := fetch
					fetch = func() (listview.Page, error) { return listview.Page{}, tt.modelError }
					m.Update(m.Init()())
					fetch = requestFetch
				}

				m.Init()
				stop()
				failure := m.Finish(outputFailure, errors.Join(tea.ErrProgramKilled, program.Err(), tt.runError))
				if !errors.Is(failure, outputFailure) || tt.wantCause != nil && !errors.Is(failure, tt.wantCause) {
					t.Fatalf("lost original failure: %v", failure)
				}
				if tt.callerErr == nil && errors.Is(failure, context.Canceled) {
					t.Errorf("cleanup became caller cancellation: %v", failure)
				}
				if tt.wantCause == pageFailure && strings.Count(failure.Error(), pageFailure.Error()) != 1 {
					t.Errorf("duplicated fetch failure: %v", failure)
				}
				var output bytes.Buffer
				if err := ShowCommandError(readableErrorTestCommand(t, "--format-error", format), failure, &output); err != nil {
					t.Fatal(err)
				}
				got := strings.TrimSpace(output.String())
				if format == "json" {
					var payload struct{ Message string }
					if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					got = payload.Message
				}
				if got != tt.want {
					t.Fatalf("diagnostic %q, want %q", got, tt.want)
				}
			})
		}
	}
}

func TestListNavigationErrorPresentation(t *testing.T) {
	origin := &listview.PaginationError{}
	for name, failure := range map[string]error{
		"direct":   origin,
		"wrapped":  fmt.Errorf("synthetic-private-cursor: %w", origin),
		"joined":   errors.Join(errors.New("synthetic-private-cursor"), origin),
		"canceled": errors.Join(origin, context.Canceled),
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(name+"/"+format, func(t *testing.T) {
				root := readableErrorTestCommand(t, "--format-error", format)
				var output bytes.Buffer
				if err := ShowCommandError(root, failure, &output); err != nil {
					t.Fatal(err)
				}
				want := origin.Error()
				if name == "canceled" {
					want = "Request canceled."
				}
				got := strings.TrimSpace(output.String())
				if format == "json" {
					var payload struct{ Message string }
					if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					got = payload.Message
				}
				if got != want || !errors.Is(failure, origin) {
					t.Fatalf("diagnostic %q, want %q; original identity=%v", got, want, errors.Is(failure, origin))
				}
				writeFailure := errors.New("synthetic diagnostic write failure")
				err := ShowCommandError(root, failure, navigationWriteFunc(func([]byte) (int, error) { return 0, writeFailure }))
				if !errors.Is(err, writeFailure) {
					t.Fatalf("lost diagnostic write failure: %v", err)
				}
			})
		}
	}
}

type navigationWriteFunc func([]byte) (int, error)

func (write navigationWriteFunc) Write(data []byte) (int, error) { return write(data) }
