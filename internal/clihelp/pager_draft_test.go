package clihelp

import (
	"bytes"
	"io"
	"os"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// The terminal and user share the same input stream. The raw paste can contain
// control-like text, and either paste delimiter can be split by the transport.
func TestHelpDraftFramesFragmentedPaste(t *testing.T) {
	reply := draftCursorReply
	paste := "first\nsecond 👩‍💻" + reply
	for split := 1; split < len(pasteOpener); split++ {
		input := &helpDraftInput{waiting: true, cursorQueries: 1}
		var output []byte
		for _, fragment := range []string{pasteOpener[:split], pasteOpener[split:] + paste, pasteCloser[:split], pasteCloser[split:]} {
			output = append(output, input.frame([]byte(fragment))...)
			if input.replied {
				t.Fatal("accepted a status report from inside a paste")
			}
		}
		if string(output) != pasteOpener+paste+pasteCloser || input.takePaste("") != paste {
			t.Fatal("changed paste content or its boundaries")
		}
		output = input.frame([]byte(reply))
		if !input.replied || !bytes.Equal(output, []byte(reply)) {
			t.Fatal("missed the terminal status reply")
		}
	}
}

func TestHelpDraftRejectsInvalidPasteAsAWhole(t *testing.T) {
	d := &helpDraft{typed: true, input: &helpDraftInput{}}
	d.appendText("echo ")
	d.input.frame([]byte(pasteOpener + "a" + "\x1b[0n" + "b" + pasteCloser))
	d.appendText(d.textFor(tea.PasteMsg{Content: "ab"}))
	if !d.invalid {
		t.Fatal("staged a command after its control bytes were removed")
	}
}

func TestHelpDraftKeepsLiteralPaste(t *testing.T) {
	const text = "café 👩‍💻 �\nsecond line"
	d := &helpDraft{typed: true, input: &helpDraftInput{}}
	d.input.frame([]byte(pasteOpener + text + pasteCloser))
	d.appendText(d.textFor(tea.PasteMsg{}))
	if d.invalid || string(d.text) != text {
		t.Fatalf("original Unicode and line endings must survive: %q", d.text)
	}
}

func TestHelpDraftWaitsForCompleteUnicode(t *testing.T) {
	const text = "é猫💻"
	input := &helpDraftInput{}
	var got []byte
	for _, b := range []byte(text) {
		chunk := input.completeUTF8([]byte{b})
		if len(chunk) > 0 && !utf8.Valid(chunk) {
			t.Fatalf("returned an incomplete character: %x", chunk)
		}
		got = append(got, chunk...)
	}
	if string(got) != text {
		t.Fatalf("got %q, want %q", got, text)
	}
}

func TestHelpDraftLeavesPostReplyBytesForShell(t *testing.T) {
	ttyInput, terminalWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ttyInput.Close()
	defer terminalWriter.Close()
	var queries bytes.Buffer
	input := &helpDraftInput{File: ttyInput, output: &queries, enabled: true}
	for i := 0; i < 2; i++ {
		if err := input.requestReply(); err != nil {
			t.Fatal(err)
		}
	}
	const consumed = "é" + draftCursorReply + "git" + draftCursorReply
	const suffix = " status"
	if _, err := io.WriteString(terminalWriter, consumed+suffix); err != nil {
		t.Fatal(err)
	}
	if err := terminalWriter.Close(); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	var buf [1]byte
	for {
		n, err := input.Read(buf[:])
		got.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != consumed {
		t.Fatalf("pager read %q, want %q", got.String(), consumed)
	}
	left, err := io.ReadAll(ttyInput)
	if err != nil || string(left) != suffix {
		t.Fatalf("shell read %q, want %q (error %v)", left, suffix, err)
	}
}
