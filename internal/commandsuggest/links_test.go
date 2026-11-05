package commandsuggest

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

const testClickPrefix = "openai-insert://Ab01234567/0123456789abcdef0123456789abcdef/"

func TestClickLinksReferenceLiteralCapturedCommands(t *testing.T) {
	capture, sink := testCollector()
	capture.clickPrefix = testClickPrefix
	root := &cli.Command{Name: "openai"}
	SetRecorder(root, capture)
	commands := []string{"openai models list", "openai responses create --input 'literal $(date); café'"}
	for _, index := range []int{0, 1, 0} {
		got := Record(root, commands[index])
		prefix := fmt.Sprintf("\x1b]8;;%s%d\x1b\\", testClickPrefix, index+1)
		if !strings.HasPrefix(got, prefix) || ansi.Strip(got) != commands[index] {
			t.Fatalf("invalid rendered command: %q", got)
		}
	}
	capture.flush()
	if sink.String() != strings.Join(commands, "\n")+"\n" {
		t.Fatalf("transport changed: %q", sink.String())
	}
	if got := Record(root, commands[0]); got != commands[0] {
		t.Fatalf("closed capture emitted link: %q", got)
	}
}

func TestClickLinksPreserveCaptureGates(t *testing.T) {
	for _, tc := range []struct {
		name, text, shell string
		terminal          bool
	}{
		{"redirected", "openai models list", "", false},
		{"wrong shell", "openai models list", "fish", true},
		{"newline", "openai models list\nwhoami", "", true},
		{"escape", "openai \x1b[2J", "", true},
		{"bidi", "openai \u202e", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture, sink := testCollector()
			capture.clickPrefix = testClickPrefix
			capture.terminal = func(io.Writer) bool { return tc.terminal }
			if got := capture.Render(io.Discard, tc.shell, tc.text); got != tc.text {
				t.Fatalf("rejected text became a link: %q", got)
			}
			capture.flush()
			if sink.Len() != 0 {
				t.Fatal("rejected command captured")
			}
		})
	}
	capture, _ := testCollector()
	capture.clickPrefix = testClickPrefix
	for index := range maxCommands {
		capture.Record(io.Discard, "", fmt.Sprintf("openai %d", index))
	}
	if got := capture.Render(io.Discard, "", "openai extra"); got != "openai extra" {
		t.Fatal("over-limit command linked")
	}
	if got := capture.Render(io.Discard, "", "openai 0"); !strings.HasPrefix(got, "\x1b]8;;") {
		t.Fatal("existing ID lost at limit")
	}
}

func TestClickPrefixRejectsMalformedIdentifiers(t *testing.T) {
	if got := clickPrefix("Ab01234567", "0123456789abcdef0123456789abcdef"); got != testClickPrefix {
		t.Fatal(got)
	}
	for _, session := range []string{"", "../tmp/abc", "abc/456789", "abc\x1b567890", strings.Repeat("a", 11)} {
		if got := clickPrefix(session, strings.Repeat("a", 32)); got != "" {
			t.Fatalf("accepted %q", session)
		}
	}
	for _, generation := range []string{"", strings.Repeat("a", 31), strings.Repeat("A", 32), strings.Repeat("a", 31) + "/"} {
		if got := clickPrefix("Ab01234567", generation); got != "" {
			t.Fatalf("accepted %q", generation)
		}
	}
}

func TestRenderTextUsesOnlyDeclaredSpansOnce(t *testing.T) {
	var recorded []string
	got := RenderText("Try openai models list; openai models. Untrusted prose: rm -rf /", []string{"openai models", "openai models list", "missing", "openai models"}, func(text string) string {
		recorded = append(recorded, text)
		return "<" + text + ">"
	})
	if got != "Try <openai models list>; <openai models>. Untrusted prose: rm -rf /" {
		t.Fatal(got)
	}
	if strings.Join(recorded, "|") != "openai models|openai models list" {
		t.Fatal(recorded)
	}
}

type shortCommandWriter struct{}

func (shortCommandWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestWriteLineInvalidatesCaptureAfterPartialWrite(t *testing.T) {
	capture, sink := testCollector()
	capture.clickPrefix = testClickPrefix
	root := &cli.Command{Name: "openai"}
	SetRecorder(root, capture)
	if err := WriteLine(root, shortCommandWriter{}, "openai failed"); err != io.ErrShortWrite {
		t.Fatalf("error=%v", err)
	}
	var output bytes.Buffer
	if err := WriteLine(root, &output, "openai models list"); err != nil {
		t.Fatal(err)
	}
	if output.String() != "openai models list\n" {
		t.Fatal(output.String())
	}
	if err := WriteLine(root, shortCommandWriter{}, "openai models list"); err != io.ErrShortWrite {
		t.Fatal(err)
	}
	capture.flush()
	if sink.Len() != 0 {
		t.Fatal(sink.String())
	}
}

func TestRenderTextDoesNotRecordHiddenPrefix(t *testing.T) {
	var recorded []string
	got := RenderText("Try openai models list", []string{"openai models", "openai models list"}, func(text string) string {
		recorded = append(recorded, text)
		return "<" + text + ">"
	})
	if got != "Try <openai models list>" || strings.Join(recorded, "|") != "openai models list" {
		t.Fatalf("text=%q commands=%v", got, recorded)
	}
}
