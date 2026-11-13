package audiomedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

type ownedPCMReader struct {
	io.Reader
	closed int
}

func (r *ownedPCMReader) Close() error { r.closed++; return nil }

func TestPlayPCMFromSeeksInMonoFrames(t *testing.T) {
	h, directory := testHelpers(t, "play")
	data := syntheticPCM(480 * 2)
	source := io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data)))
	if err := playPCMFrom(t.Context(), source, 48000, 1, 5*time.Millisecond, h); err != nil {
		t.Fatal(err)
	}
	record := readHelperRecord(t, directory, "ffplay")
	assertInputBoundary(t, record.Args, "s16le")
	rate := slices.Index(record.Args, "-sample_rate")
	recorded, err := hex.DecodeString(record.Input)
	if err != nil || rate < 0 || record.Args[rate+1] != "48000" || !bytes.Equal(recorded, data[240*2:]) {
		t.Fatal("mono replay changed the sample rate or seek position")
	}
}

func TestPlayPCMFromSeeksInStereoFrames(t *testing.T) {
	h, directory := testHelpers(t, "play")
	data := syntheticPCM(480 * 4)
	source := io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data)))
	if err := playPCMFrom(t.Context(), source, 48000, 2, 5*time.Millisecond, h); err != nil {
		t.Fatal(err)
	}
	recorded, err := hex.DecodeString(readHelperRecord(t, directory, "ffplay").Input)
	if err != nil || len(recorded) < 44 || !bytes.Equal(recorded[44:], data[240*4:]) {
		t.Fatal("replay started on the wrong sample frame")
	}
	if err := PlayPCMFrom(t.Context(), source, 48000, 2, time.Second); err != nil {
		t.Fatalf("seeking beyond the end: %v", err)
	}
	if err := PlayPCMFrom(t.Context(), io.NewSectionReader(source, 0, 3), 48000, 2, time.Second); !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("incomplete stereo frame: %v", err)
	}
}

func TestPlayPCMPreservesStreamMetadataAndBytes(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-not-for-helper")
	t.Setenv("FFREPORT", "synthetic-not-for-helper")
	for _, channels := range []int{1, 2} {
		h, directory := testHelpers(t, "play")
		body := syntheticPCM(4800 * channels * 2)
		source := &ownedPCMReader{Reader: bytes.NewReader(body)}
		if err := playPCM(t.Context(), source, 48000, channels, h); err != nil {
			t.Fatal(err)
		}
		if source.closed != 1 {
			t.Fatalf("source closed %d times", source.closed)
		}
		record := readHelperRecord(t, directory, "ffplay")
		if record.CredentialPresent || record.ReportPresent {
			t.Fatal("credentials or FFREPORT reached player")
		}
		data, err := hex.DecodeString(record.Input)
		if err != nil {
			t.Fatal(err)
		}
		if channels == 1 {
			assertInputBoundary(t, record.Args, "s16le")
			rate := slices.Index(record.Args, "-sample_rate")
			if rate < 0 || record.Args[rate+1] != "48000" || !slices.Contains(record.Args, "-nofind_stream_info") || !bytes.Equal(data, body) {
				t.Fatal("mono playback metadata, startup options, or samples changed")
			}
		} else {
			assertInputBoundary(t, record.Args, "wav")
			if len(data) != 44+len(body) || string(data[:4]) != "RIFF" || string(data[8:16]) != "WAVEfmt " ||
				binary.LittleEndian.Uint32(data[24:]) != 48000 || int(binary.LittleEndian.Uint16(data[22:])) != channels || !bytes.Equal(data[44:], body) {
				t.Fatal("multichannel playback header or samples changed")
			}
		}
	}
}

func TestPlayPCMRejectsIncompleteFramesAndInvalidMetadata(t *testing.T) {
	for _, tc := range []struct{ rate, channels, bytes int }{{24000, 1, 3}, {48000, 2, 6}, {0, 1, 4}, {24000, 0, 4}, {24000, 32768, 4}, {int(^uint(0) >> 1), 2, 4}} {
		h, _ := testHelpers(t, "play")
		source := &ownedPCMReader{Reader: bytes.NewReader(make([]byte, tc.bytes))}
		if err := playPCM(t.Context(), source, tc.rate, tc.channels, h); !errors.Is(err, ErrInvalidAudio) {
			t.Fatalf("invalid input %+v: %v", tc, err)
		}
		if source.closed != 1 {
			t.Fatal("invalid input source ownership lost")
		}
	}
}

type blockedPCMReader struct {
	started, closed     chan struct{}
	readOnce, closeOnce sync.Once
	readyFile           string
	closeReturnsEOF     bool
}

func (r *blockedPCMReader) Read([]byte) (int, error) {
	r.readOnce.Do(func() { close(r.started) })
	if r.readyFile != "" {
		if err := os.WriteFile(r.readyFile, nil, 0600); err != nil {
			return 0, err
		}
	}
	<-r.closed
	if r.closeReturnsEOF {
		return 0, io.EOF
	}
	return 0, io.ErrClosedPipe
}
func (r *blockedPCMReader) Close() error { r.closeOnce.Do(func() { close(r.closed) }); return nil }

func TestPlayPCMCancellationClosesBlockedInputAndReapsHelper(t *testing.T) {
	h, directory, child := nativePCMHelpers(t, "hold")
	source := &blockedPCMReader{started: make(chan struct{}), closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- playPCM(ctx, source, 24000, 1, h) }()
	waitForHelper(t, directory, "ffplay", done)
	select {
	case <-source.started:
	case <-time.After(3 * time.Second):
		t.Fatal("player did not read source")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || (*child).ProcessState == nil {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("player or input copier did not finish")
	}
	select {
	case <-source.closed:
	default:
		t.Fatal("input remained open")
	}
}

func TestPlayPCMEarlyPlayerExitReleasesInput(t *testing.T) {
	h, _, child := nativePCMHelpers(t, "early")
	source := &blockedPCMReader{started: make(chan struct{}), closed: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := playPCM(ctx, source, 24000, 1, h)
	if !errors.Is(err, ErrPlaybackFailed) || ctx.Err() != nil || (*child).ProcessState == nil {
		t.Fatalf("early player exit: %v", err)
	}
	select {
	case <-source.closed:
	default:
		t.Fatal("early exit retained input")
	}
}

func TestPlayPCMEarlyPlayerExitCannotManufactureSuccessfulEOF(t *testing.T) {
	h, directory, child := nativePCMHelpers(t, "early-after-read")
	source := &blockedPCMReader{started: make(chan struct{}), closed: make(chan struct{}),
		readyFile: filepath.Join(directory, "source-ready"), closeReturnsEOF: true}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := playPCM(ctx, source, 24000, 1, h)
	if !errors.Is(err, ErrPlaybackFailed) || ctx.Err() != nil || (*child).ProcessState == nil {
		t.Fatalf("forced close was mistaken for completed playback: %v", err)
	}
}

func TestPlayPCMMissingHelperClosesSource(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	source := &ownedPCMReader{Reader: bytes.NewReader([]byte{0, 0})}
	if err := PlayPCM(t.Context(), source, 24000, 1); !errors.Is(err, ErrPlayerUnavailable) || source.closed != 1 {
		t.Fatalf("missing helper: %v; closes=%d", err, source.closed)
	}
}
