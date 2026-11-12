package audiomedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func nativePCMHelpers(t *testing.T, mode string) (helpers, string, **exec.Cmd) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	var child *exec.Cmd
	return helpers{
		find: func(name string) (string, error) { return name, nil },
		command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			command := []string{"-test.run=^TestNativePCMHelperProcess$", "--", mode, directory, name}
			child = exec.CommandContext(ctx, binary, append(command, args...)...)
			return child
		},
	}, directory, &child
}

func syntheticPCM(length int) []byte {
	data := make([]byte, length)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

// This child emits synthetic PCM. It never discovers or opens an audio device.
func TestNativePCMHelperProcess(t *testing.T) {
	marker := slices.Index(os.Args, "--")
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	mode, directory, tool := args[0], args[1], args[2]
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "FFREPORT"} {
		if os.Getenv(name) != "" {
			os.Exit(20)
		}
	}
	if mode == "early" {
		os.Exit(0)
	}
	if mode == "early-after-read" {
		for {
			if _, err := os.Stat(filepath.Join(directory, "source-ready")); err == nil {
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if mode != "hold" {
		length := 2*captureChunkBytes + 6
		if mode == "partial-hold" || mode == "failure" {
			length = 6
		}
		if mode == "odd" {
			length = 5
		}
		if mode == "empty" {
			length = 0
		}
		data := syntheticPCM(length)
		for len(data) > 0 {
			n := min(137, len(data))
			if _, err := os.Stdout.Write(data[:n]); err != nil {
				os.Exit(21)
			}
			data = data[n:]
		}
	}
	record, _ := json.Marshal(helperRecord{Args: args[3:]})
	if err := os.WriteFile(filepath.Join(directory, tool+".json"), record, 0600); err != nil {
		os.Exit(22)
	}
	if mode == "failure" {
		fmt.Fprintln(os.Stderr, "synthetic private device/path diagnostic")
		os.Exit(7)
	}
	if mode == "hold" || mode == "partial-hold" {
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}

func TestCapturePCMPlatformDeviceArguments(t *testing.T) {
	for _, tc := range []struct{ platform, device, backend, input string }{
		{"darwin", "", "avfoundation", ":default"},
		{"darwin", "2", "avfoundation", ":2"},
		{"darwin", "Microphone With Spaces", "avfoundation", ":Microphone With Spaces"},
		{"linux", "", "pulse", "default"},
		{"linux", "recording.monitor", "pulse", "recording.monitor"},
		{"windows", "Microphone With Spaces", "dshow", "audio=Microphone With Spaces"},
	} {
		t.Run(tc.platform+"/"+tc.device, func(t *testing.T) {
			args, err := captureArguments(tc.platform, tc.device)
			if err != nil {
				t.Fatal(err)
			}
			input := slices.Index(args, "-i")
			backend := slices.Index(args, "-f")
			if input < 0 || args[input+1] != tc.input || backend < 0 || args[backend+1] != tc.backend {
				t.Fatalf("device selection: %v", args)
			}
			for option, value := range map[string]string{"-map": "0:a:0", "-ac": "1", "-ar": "24000", "-c:a": "pcm_s16le"} {
				at := slices.Index(args, option)
				if at < 0 || args[at+1] != value {
					t.Fatalf("missing %s %s: %v", option, value, args)
				}
			}
			if args[len(args)-1] != "pipe:1" || !slices.Contains(args, "-nostdin") || !slices.Contains(args, "-vn") {
				t.Fatalf("capture boundary: %v", args)
			}
		})
	}
	for _, tc := range []struct{ platform, device string }{{"windows", ""}, {"windows", "private:video=Camera"}, {"freebsd", ""}, {"darwin", "private\nname"}, {"linux", "\x00"}} {
		if _, err := captureArguments(tc.platform, tc.device); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("invalid device accepted or echoed: %v", err)
		}
	}
}

func TestCapturePCMRetainsOwnedChunksAndFinalSamples(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "FFREPORT"} {
		t.Setenv(name, "synthetic-not-for-helper")
	}
	h, directory, child := nativePCMHelpers(t, "capture")
	var chunks [][]byte
	err := capturePCM(t.Context(), "Microphone With Spaces", func(data []byte) error {
		chunks = append(chunks, data)
		return nil
	}, "darwin", h)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 || len(chunks[0]) != captureChunkBytes || len(chunks[1]) != captureChunkBytes || len(chunks[2]) != 6 {
		t.Fatalf("chunk lengths: %v", chunks)
	}
	if !bytes.Equal(bytes.Join(chunks, nil), syntheticPCM(2*captureChunkBytes+6)) {
		t.Fatal("captured samples changed or an earlier callback buffer was reused")
	}
	if (*child).ProcessState == nil {
		t.Fatal("capture returned without waiting for its helper")
	}
	record := readHelperRecord(t, directory, "ffmpeg")
	if !slices.Contains(record.Args, ":Microphone With Spaces") {
		t.Fatal("device was not passed as one audio-only argument")
	}
}

func TestCapturePCMPartialFailuresRetainCompleteSamples(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		want  error
		bytes int
	}{{"odd", ErrInvalidAudio, 4}, {"failure", ErrCaptureFailed, 6}, {"empty", ErrCaptureFailed, 0}} {
		t.Run(tc.mode, func(t *testing.T) {
			h, _, _ := nativePCMHelpers(t, tc.mode)
			var received []byte
			err := capturePCM(t.Context(), "", func(data []byte) error { received = append(received, data...); return nil }, "linux", h)
			if !errors.Is(err, tc.want) || !bytes.Equal(received, syntheticPCM(tc.bytes)) || strings.Contains(err.Error(), "private") {
				t.Fatalf("result: bytes=%d error=%v", len(received), err)
			}
		})
	}
}

func TestCapturePCMCallbackFailureStopsHelper(t *testing.T) {
	h, _, child := nativePCMHelpers(t, "capture")
	failure := errors.New("synthetic upload rejected")
	calls := 0
	err := capturePCM(t.Context(), "", func([]byte) error { calls++; return failure }, "darwin", h)
	if !errors.Is(err, failure) || calls != 1 || (*child).ProcessState == nil {
		t.Fatalf("callback result: calls=%d error=%v", calls, err)
	}
}

func TestCapturePCMCancellationDrainsFinalSamplesAndReapsHelper(t *testing.T) {
	h, directory, child := nativePCMHelpers(t, "partial-hold")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	var received []byte
	go func() {
		done <- capturePCM(ctx, "", func(data []byte) error { received = append(received, data...); return nil }, "darwin", h)
	}()
	waitForHelper(t, directory, "ffmpeg", done)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !bytes.Equal(received, syntheticPCM(6)) || (*child).ProcessState == nil {
			t.Fatalf("cancel: bytes=%d error=%v", len(received), err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not release its helper")
	}
}

func TestCapturePCMCancellationPreservesFinalCallbackFailure(t *testing.T) {
	h, directory, _ := nativePCMHelpers(t, "partial-hold")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failure := errors.New("synthetic final upload failed")
	done := make(chan error, 1)
	go func() {
		done <- capturePCM(ctx, "", func([]byte) error { return failure }, "darwin", h)
	}()
	waitForHelper(t, directory, "ffmpeg", done)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
			t.Fatalf("final upload failure disappeared during cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not finish its final callback")
	}
}

func TestNativePCMPreflightDoesNotOpenDevices(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := ValidateCapture(""); !errors.Is(err, ErrCaptureUnavailable) && !strings.Contains(err.Error(), "Windows microphone") {
		t.Fatalf("capture discovery: %v", err)
	}
	if err := ValidatePlayback(); !errors.Is(err, ErrPlayerUnavailable) {
		t.Fatalf("playback discovery: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CapturePCM(ctx, "", func([]byte) error { t.Fatal("canceled capture ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture: %v", err)
	}
}
