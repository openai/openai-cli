package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
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
