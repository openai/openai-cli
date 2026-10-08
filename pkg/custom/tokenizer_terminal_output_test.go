package custom

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() {
	if len(os.Args) == 3 && os.Args[1] == "__tokenizer-terminal-parent" {
		writer, err := newTokenizerTerminalOutput(context.Background(), os.Args[2], os.Stdout)
		if err != nil {
			os.Exit(6)
		}
		fmt.Fprintln(os.Stderr, writer.worker.command.Process.Pid)
		go func() {
			_, _ = writer.WriteContext(context.Background(), bytes.Repeat([]byte("p"), 4*tokenizerTerminalFrameBytes))
		}()
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(7)
	}
	if len(os.Args) != 3 || os.Args[1] != "tokenizer" || os.Args[2] != "__output" {
		return
	}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if !strings.EqualFold(name, "GOMAXPROCS") && !strings.EqualFold(name, "SystemRoot") {
			os.Exit(8)
		}
	}
	mode := filepath.Base(os.Args[0])
	if strings.Contains(mode, "silent-ready") {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	if strings.Contains(mode, "invalid-ready") {
		_, _ = os.Stderr.Write([]byte("badready"))
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	if strings.Contains(mode, "invalid-ack") || strings.Contains(mode, "exit-before-ack") {
		_, _ = os.Stderr.Write(tokenizerTerminalReady[:])
		var header [8]byte
		_, _ = io.ReadFull(os.Stdin, header[:])
		_, _ = io.CopyN(io.Discard, os.Stdin, int64(binary.BigEndian.Uint32(header[4:])))
		if strings.Contains(mode, "invalid-ack") {
			_, _ = os.Stderr.Write(make([]byte, 9))
			_, _ = io.Copy(io.Discard, os.Stdin)
		}
		os.Exit(9)
	}
	var output io.Writer = os.Stdout
	if strings.Contains(mode, "short-write") {
		output = tokenizerTerminalShortWriter{os.Stdout}
	}
	err := serveTokenizerTerminalOutput(context.Background(), os.Stdin, output, os.Stderr)
	if strings.Contains(mode, "completion") {
		_ = os.WriteFile(filepath.Join(filepath.Dir(os.Args[0]), "completed"), []byte("stopped"), 0600)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type tokenizerTerminalShortWriter struct{ io.Writer }

func (w tokenizerTerminalShortWriter) Write(data []byte) (int, error) {
	return w.Writer.Write(data[:len(data)/2])
}

func tokenizerTerminalTestExecutable(t *testing.T, mode string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokenizer-output-"+mode+".exe")
	if err := os.Link(os.Args[0], path); err == nil {
		return path
	}
	input, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	if err := errors.Join(copyErr, output.Close()); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokenizerTerminalOutputExactBytesAndMinimalEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-not-a-key")
	t.Setenv("SYNTHETIC_UNRELATED_SECRET", "synthetic-private-value")
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer, err := newTokenizerTerminalOutput(t.Context(), os.Args[0], file)
	if err != nil {
		t.Fatal(err)
	}
	worker := writer.worker
	want := bytes.Repeat([]byte("UTF-8 👩‍💻\r\n\x00\x1b[0m"), 5000)
	for _, data := range [][]byte{nil, want[:3], want[3:]} {
		if n, err := writer.WriteContext(t.Context(), data); err != nil || n != len(data) {
			t.Fatalf("write returned %d, %v", n, err)
		}
	}
	if err := writer.Close(); err != nil || worker.command.ProcessState == nil {
		t.Fatalf("writer did not reap its child: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file.Name())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("terminal byte stream changed: %v", err)
	}
	if _, err := writer.WriteContext(t.Context(), []byte("late")); err == nil {
		t.Fatal("closed writer accepted a frame")
	}
}

func TestTokenizerTerminalOutputCancelBlockedWriteAndRestart(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	writer, err := newTokenizerTerminalOutput(t.Context(), os.Args[0], write)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	first := writer.worker
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	started := time.Now()
	data := bytes.Repeat([]byte("x"), 128*tokenizerTerminalFrameBytes)
	n, err := writer.WriteContext(ctx, data)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || n == len(data) || time.Since(started) > 3*time.Second {
		t.Fatalf("blocked write did not stop: %d, %v", n, err)
	}
	if first.command.ProcessState == nil || writer.worker != nil {
		t.Fatal("canceled worker was not reaped and retired")
	}
	drained := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, read); drained <- err }()
	cleanup, finish := context.WithTimeout(t.Context(), 2*time.Second)
	defer finish()
	if n, err := writer.WriteContext(cleanup, []byte("cleanup")); err != nil || n != 7 {
		t.Fatalf("independent cleanup could not restart the writer: %d, %v", n, err)
	}
	second := writer.worker
	if second == first {
		t.Fatal("cleanup reused the retired worker")
	}
	if err := writer.Close(); err != nil || second.command.ProcessState == nil {
		t.Fatalf("replacement was not reaped: %v", err)
	}
	write.Close()
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
}

func TestTokenizerTerminalOutputStartupAndAcknowledgementFailures(t *testing.T) {
	for _, mode := range []string{"silent-ready", "invalid-ready", "invalid-ack", "exit-before-ack", "short-write"} {
		t.Run(mode, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "output")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			timeout := 5 * time.Second
			if mode == "silent-ready" {
				timeout = 150 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			writer, err := newTokenizerTerminalOutput(ctx, tokenizerTerminalTestExecutable(t, mode), file)
			if strings.Contains(mode, "ready") {
				if err == nil || writer != nil {
					t.Fatal("invalid startup succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			worker := writer.worker
			n, err := writer.WriteContext(ctx, []byte("partial output"))
			if err == nil || n == len("partial output") || worker.command.ProcessState == nil {
				t.Fatalf("worker failure disappeared or was not reaped: %d, %v", n, err)
			}
			if mode == "short-write" && n != len("partial output")/2 {
				t.Fatalf("partial byte count was lost: %d", n)
			}
		})
	}
}

func TestTokenizerTerminalOutputRejectsMalformedFrames(t *testing.T) {
	for _, fields := range [][2]uint32{{0, 1}, {1, 0}, {1, tokenizerTerminalFrameBytes + 1}, {2, 1}, {1, 5}} {
		var header [8]byte
		binary.BigEndian.PutUint32(header[:4], fields[0])
		binary.BigEndian.PutUint32(header[4:], fields[1])
		if err := readTokenizerTerminalFrames(bytes.NewReader(header[:]), make(chan tokenizerTerminalFrame, 1)); err == nil {
			t.Fatalf("malformed frame succeeded: %v", fields)
		}
	}
	var stream bytes.Buffer
	for sequence := uint32(1); sequence <= 2; sequence++ {
		_ = binary.Write(&stream, binary.BigEndian, sequence)
		_ = binary.Write(&stream, binary.BigEndian, uint32(1))
		stream.WriteByte('x')
	}
	if err := readTokenizerTerminalFrames(&stream, make(chan tokenizerTerminalFrame, 1)); err == nil {
		t.Fatal("unacknowledged frames blocked the lifeline reader")
	}
}

func TestTokenizerTerminalOutputParentDeathStopsBlockedWriter(t *testing.T) {
	helper := tokenizerTerminalTestExecutable(t, "completion")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	parent := exec.CommandContext(ctx, os.Args[0], "__tokenizer-terminal-parent", helper)
	parent.Stdout = write
	parent.Env = tokenizerTerminalEnvironment()
	input, err := parent.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	status, err := parent.StderrPipe()
	if err != nil || parent.Start() != nil {
		t.Fatalf("start parent: %v", err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	var childPID int
	if _, err := fmt.Fscanln(status, &childPID); err != nil {
		t.Fatal(err)
	}
	child, err := os.FindProcess(childPID)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Release()
	completed := filepath.Join(filepath.Dir(helper), "completed")
	defer func() {
		if _, err := os.Stat(completed); err != nil {
			_ = child.Kill()
		}
	}()
	// A byte proves a terminal write began. Keep the read end open but stop
	// draining; parent death must stop the writer without relying on EPIPE.
	var first [1]byte
	if _, err := io.ReadFull(read, first[:]); err != nil {
		t.Fatal(err)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(completed); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked writer survived its parent's death")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTokenizerTerminalOutputCloseStopsActiveWrite(t *testing.T) {
	before := runtime.NumGoroutine()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	writer, err := newTokenizerTerminalOutput(t.Context(), os.Args[0], write)
	if err != nil {
		t.Fatal(err)
	}
	worker := writer.worker
	finished := make(chan error, 1)
	go func() {
		_, err := writer.WriteContext(t.Context(), bytes.Repeat([]byte("x"), 128*tokenizerTerminalFrameBytes))
		finished <- err
	}()
	var first [1]byte
	if _, err := io.ReadFull(read, first[:]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err == nil || worker.command.ProcessState == nil {
		t.Fatalf("Close did not interrupt and reap active output: %v", err)
	}
	if runtime.NumGoroutine() > before+2 {
		t.Fatal("output cancellation retained parent goroutines")
	}
}
