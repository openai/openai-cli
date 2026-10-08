package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestReportSaveReceiptPolicy(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		want  bool
	}{
		{nil, true}, {[]string{"--quiet"}, false}, {[]string{"--verbose"}, true},
		{[]string{"--quiet", "--verbose"}, false}, {[]string{"--quiet=false"}, true},
		{[]string{"--format", "json"}, false}, {[]string{"--format", "jsonl"}, false},
		{[]string{"--format", "raw"}, false}, {[]string{"--format", "yaml"}, false},
		{[]string{"--format-error", "json"}, false}, {[]string{"--transform-error", "message"}, false},
		{[]string{"--format", "json", "--format-error", "text"}, true},
		{[]string{"--format-error", "auto"}, true},
	} {
		t.Run(strings.Join(tc.flags, " "), func(t *testing.T) {
			var out bytes.Buffer
			cmd := receiptTestCommand(func(cmd *cli.Command) error {
				return ReportSaveReceipt(cmd, &out, "Wrote output to: synthetic.bin", nil)
			})
			if err := cmd.Run(context.Background(), append([]string{"openai"}, tc.flags...)); err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.want {
				want = "Wrote output to: synthetic.bin\n"
			}
			if out.String() != want {
				t.Fatalf("receipt=%q want=%q", out.String(), want)
			}
		})
	}
}

func receiptTestCommand(action func(*cli.Command) error) *cli.Command {
	return &cli.Command{Name: "openai", Flags: []cli.Flag{
		&cli.BoolFlag{Name: "quiet"}, &cli.BoolFlag{Name: "verbose"},
		&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"},
	}, Action: func(_ context.Context, cmd *cli.Command) error { return action(cmd) }}
}

type receiptFailWriter struct {
	err    error
	writes int
}

func (w *receiptFailWriter) Write(_ []byte) (int, error) { w.writes++; return 0, w.err }

func TestReportSaveReceiptFailures(t *testing.T) {
	sourceErr := errors.New("synthetic source failure")
	sinkErr := io.ErrClosedPipe
	sink := &receiptFailWriter{err: sinkErr}
	cmd := receiptTestCommand(func(cmd *cli.Command) error {
		if err := ReportSaveReceipt(cmd, sink, "must not claim success", sourceErr); !errors.Is(err, sourceErr) {
			t.Fatalf("source error=%v", err)
		}
		if sink.writes != 0 {
			t.Fatal("failed operation wrote success receipt")
		}
		if err := ReportSaveReceipt(cmd, sink, "", nil); err != nil || sink.writes != 0 {
			t.Fatal("empty receipt wrote bytes")
		}
		err := ReportSaveReceipt(cmd, sink, "Wrote output", nil)
		var receiptErr *saveReceiptError
		if !errors.Is(err, sinkErr) || !errors.As(err, &receiptErr) {
			t.Fatalf("receipt failure=%v", err)
		}
		return nil
	})
	if err := cmd.Run(context.Background(), []string{"openai"}); err != nil {
		t.Fatal(err)
	}
}

func TestReportSaveReceiptEscapesTerminalControls(t *testing.T) {
	var out bytes.Buffer
	cmd := receiptTestCommand(func(cmd *cli.Command) error {
		return ReportSaveReceipt(cmd, &out, "Wrote output to: synthetic\x1b[31m.bin", nil)
	})
	if err := cmd.Run(context.Background(), []string{"openai"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatalf("unescaped receipt=%q", out.String())
	}
}

func TestSaveReceiptContextPreservesRequestState(t *testing.T) {
	for _, finalRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted response request", true: "final response request"}[finalRequest], func(t *testing.T) {
			cmd := receiptTestCommand(func(cmd *cli.Command) error {
				middleware := captureSaveReceiptPolicy(cmd)
				if err := cmd.Set("format-error", "json"); err != nil {
					return err
				}
				type markerKey struct{}
				ctx, cancel := context.WithCancel(context.WithValue(context.Background(), markerKey{}, "initial"))
				defer cancel()
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/synthetic", nil)
				if err != nil {
					return err
				}
				original := &http.Response{StatusCode: http.StatusOK}
				response, err := middleware(request, func(tagged *http.Request) (*http.Response, error) {
					if tagged == request || tagged.Context().Value(markerKey{}) != "initial" {
						t.Fatal("middleware replaced request state or mutated the request")
					}
					if finalRequest {
						original.Request = tagged.WithContext(context.WithValue(tagged.Context(), markerKey{}, "final"))
					}
					return original, nil
				})
				if err != nil || response == original || response.Request == nil {
					t.Fatalf("response ownership or request propagation: %v", err)
				}
				policy, ok := response.Request.Context().Value(saveReceiptPolicyKey{}).(saveReceiptPolicy)
				if !ok || !policy.enabled || policy.stderr != os.Stderr {
					t.Fatal("receipt policy changed with later command flags")
				}
				want := "initial"
				if finalRequest {
					want = "final"
					if response.Request == original.Request {
						t.Fatal("middleware reused the final request")
					}
				}
				if response.Request.Context().Value(markerKey{}) != want {
					t.Fatal("middleware lost endpoint context")
				}
				cancel()
				if !errors.Is(response.Request.Context().Err(), context.Canceled) {
					t.Fatal("middleware detached cancellation")
				}
				return nil
			})
			if err := cmd.Run(context.Background(), []string{"openai"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type receiptCloseBody struct {
	io.Reader
	closed bool
	err    error
}

func (b *receiptCloseBody) Close() error { b.closed = true; return b.err }

type receiptWriterFunc func([]byte) (int, error)

func (f receiptWriterFunc) Write(data []byte) (int, error) { return f(data) }

func TestResponseSaveReceiptCompletion(t *testing.T) {
	closeFailure := errors.New("synthetic late close failure")
	for _, name := range []string{"success", "disabled", "receipt failure", "source close failure", "library caller"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "output.bin")
			if err := os.WriteFile(path, []byte("GOOD"), 0o600); err != nil {
				t.Fatal(err)
			}
			body := &receiptCloseBody{Reader: strings.NewReader("complete")}
			if name == "source close failure" {
				body.err = closeFailure
			}
			var receipt bytes.Buffer
			writes := 0
			writer := receiptWriterFunc(func(data []byte) (int, error) {
				writes++
				if !body.closed {
					t.Fatal("receipt preceded source closure")
				}
				if saved, err := os.ReadFile(path); err != nil || string(saved) != "complete" {
					t.Fatalf("receipt preceded completed save: %q %v", saved, err)
				}
				requireNoDownloadStages(t, filepath.Dir(path))
				if name == "receipt failure" {
					return 0, io.ErrClosedPipe
				}
				return receipt.Write(data)
			})
			ctx := context.Background()
			if name != "library caller" {
				ctx = context.WithValue(ctx, saveReceiptPolicyKey{}, saveReceiptPolicy{enabled: name != "disabled", stderr: writer})
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/synthetic", nil)
			if err != nil {
				t.Fatal(err)
			}
			message, err := WriteBinaryResponse(&http.Response{Body: body, Request: request}, io.Discard, path)
			switch name {
			case "source close failure":
				if !errors.Is(err, closeFailure) || writes != 0 {
					t.Fatalf("close failure=%v receipt writes=%d", err, writes)
				}
			case "receipt failure":
				var failure *saveReceiptError
				if !errors.Is(err, io.ErrClosedPipe) || !errors.As(err, &failure) || writes != 1 {
					t.Fatalf("receipt failure=%v writes=%d", err, writes)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if name == "library caller" {
				if message != "Wrote output to: "+path || writes != 0 {
					t.Fatalf("library contract: message=%q writes=%d", message, writes)
				}
			} else if message != "" {
				t.Fatalf("handled receipt leaked to generated caller: %q", message)
			}
			if name == "success" && receipt.String() != "Wrote output to: "+path+"\n" {
				t.Fatalf("receipt=%q", receipt.String())
			}
			if name == "disabled" && writes != 0 {
				t.Fatal("disabled receipt emitted bytes")
			}
			want := "complete"
			if name == "source close failure" {
				want = "GOOD"
			}
			if saved, err := os.ReadFile(path); err != nil || string(saved) != want {
				t.Fatalf("saved=%q want=%q error=%v", saved, want, err)
			}
		})
	}
}
