//go:build darwin || linux

package custom

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestTokenizerTerminalOutputLifelineStopsSaturatedPipe(t *testing.T) {
	helper := tokenizerTerminalTestExecutable(t, "native-completion")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	fd := int(write.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	// Fill with writes larger than a helper frame, until the kernel refuses
	// another byte. The read end stays open and unread until the helper exits.
	fill := bytes.Repeat([]byte("p"), 4*tokenizerTerminalFrameBytes)
	for {
		n, err := unix.Write(fd, fill)
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil || n <= 0 {
			t.Fatalf("saturate output pipe: %d, %v", n, err)
		}
	}
	if _, err := unix.Write(fd, []byte("p")); !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("output pipe did not stay saturated: %v", err)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, helper, "tokenizer", "__output")
	command.Stdout, command.Env = write, tokenizerTerminalEnvironment()
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	ack, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = command.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			_ = command.Process.Kill()
			<-done
		}
	}()
	var ready [8]byte
	if _, err := io.ReadFull(ack, ready[:]); err != nil || ready != tokenizerTerminalReady {
		t.Fatalf("helper readiness: %v", err)
	}
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], 1)
	binary.BigEndian.PutUint32(header[4:], tokenizerTerminalFrameBytes)
	if err := writeTokenizerTerminalBytes(input, header[:]); err != nil {
		t.Fatal(err)
	}
	if err := writeTokenizerTerminalBytes(input, fill[:tokenizerTerminalFrameBytes]); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(filepath.Dir(helper), "started")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("helper exited before starting output: %v", waitErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start output")
		}
		time.Sleep(time.Millisecond)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("lifeline EOF did not produce a clean helper exit: %v", waitErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper survived lifeline EOF with saturated output")
	}
}
