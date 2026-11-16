package audiomedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type helperRecord struct {
	Args              []string
	Input             string
	ReportPresent     bool
	CredentialPresent bool
}

func testHelpers(t *testing.T, mode string) (helpers, string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	return helpers{
		find: func(name string) (string, error) { return name, nil },
		command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			command := []string{"-test.run=^TestAudioMediaHelperProcess$", "--", mode, directory, name}
			return exec.CommandContext(ctx, binary, append(command, args...)...)
		},
	}, directory
}

// This subprocess is the fake decoder/player. No native helper or device runs.
func TestAudioMediaHelperProcess(t *testing.T) {
	index := slices.Index(os.Args, "--")
	if index < 0 {
		return
	}
	args := os.Args[index+1:]
	mode, directory, tool := args[0], args[1], args[2]
	if mode == "player-error" && tool == "ffplay" {
		fmt.Fprint(os.Stderr, "private device failed\n")
		os.Exit(0)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(9)
	}
	record := helperRecord{Args: args[3:], Input: hex.EncodeToString(input),
		ReportPresent: os.Getenv("FFREPORT") != "", CredentialPresent: os.Getenv("OPENAI_API_KEY") != ""}
	raw, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(directory, tool+".json"), raw, 0600); err != nil {
		os.Exit(8)
	}
	switch mode {
	case "hold", "player-error":
		for {
			time.Sleep(time.Hour)
		}
	case "stderr":
		for range 200 {
			fmt.Fprint(os.Stderr, "/private/sensitive/audio\x1b[31m "+strings.Repeat("x", 1024))
		}
		os.Exit(0)
	case "failure":
		os.Exit(7)
	case "malformed":
		fmt.Fprint(os.Stdout, "not audio")
		os.Exit(0)
	}
	if tool == "ffmpeg" {
		// Opposite-phase stereo tests channel preservation and frame duration.
		body := testWave(1, 2, 16, 48000, []byte{0, 128, 255, 127, 0, 0, 0, 0})
		binary.LittleEndian.PutUint32(body[4:], math.MaxUint32)
		binary.LittleEndian.PutUint32(body[40:], math.MaxUint32)
		if mode == "partial" {
			body = body[:len(body)-1]
		}
		if _, err := os.Stdout.Write(body); err != nil {
			os.Exit(6)
		}
	}
	os.Exit(0)
}

func readHelperRecord(t *testing.T, directory, tool string) helperRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, tool+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record helperRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func assertInputBoundary(t *testing.T, args []string, demuxer string) {
	t.Helper()
	protocol := slices.Index(args, "-protocol_whitelist")
	if protocol < 0 || args[protocol+1] != "pipe" {
		t.Fatalf("protocol boundary: %v", args)
	}
	input := slices.Index(args, "-i")
	if input < 0 || args[input+1] != "pipe:0" {
		t.Fatalf("input boundary: %v", args)
	}
	format := slices.Index(args[:input], "-f")
	if format < 0 || args[format+1] != demuxer {
		t.Fatalf("forced demuxer: %v", args)
	}
	for _, arg := range args {
		if strings.Contains(arg, "/") || strings.Contains(arg, "https:") {
			t.Fatalf("path reached helper: %v", args)
		}
	}
}

func TestAnalyzeCompressedFormatsUseVerifiedBytesAndSafeHelpers(t *testing.T) {
	t.Setenv("FFREPORT", "file=/private/must-not-exist")
	t.Setenv("OPENAI_API_KEY", "synthetic-must-not-reach-helper")
	for _, format := range []string{"mp3", "opus", "aac", "flac"} {
		t.Run(format, func(t *testing.T) {
			h, directory := testHelpers(t, "decode")
			body := []byte("synthetic compressed fixture")
			file := testFile(t, body)
			if _, err := file.Seek(9, 0); err != nil {
				t.Fatal(err)
			}
			result, err := analyze(t.Context(), file, format, h)
			if err != nil {
				t.Fatal(err)
			}
			if result.Bytes != int64(len(body)) || result.Duration != 2.0/48000 || result.Peaks[0] != 1 {
				t.Fatalf("decoded analysis: %+v", result)
			}
			record := readHelperRecord(t, directory, "ffmpeg")
			demux := format
			if format == "opus" {
				demux = "ogg"
			}
			assertInputBoundary(t, record.Args, demux)
			if record.Input != hex.EncodeToString(body) || record.ReportPresent || record.CredentialPresent {
				t.Fatalf("helper input/environment: %+v", record)
			}
			if slices.Contains(record.Args, "-ac") || slices.Contains(record.Args, "-ar") {
				t.Fatal("analysis alters channels or sample rate")
			}
			if position, err := file.Seek(0, 1); err != nil || position != 9 {
				t.Fatalf("caller file changed: %d %v", position, err)
			}
		})
	}
}

func TestAnalyzeWaveFallbackPreservesUnknownLengthAndUnsupportedEncoding(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		body := testWave(1, 1, 16, 24000, []byte{0, 0})
		if streaming {
			binary.LittleEndian.PutUint32(body[4:], math.MaxUint32)
			binary.LittleEndian.PutUint32(body[40:], math.MaxUint32)
		} else {
			binary.LittleEndian.PutUint16(body[20:], 6)
		}
		h, directory := testHelpers(t, "decode")
		if _, err := analyze(t.Context(), testFile(t, body), "wav", h); err != nil {
			t.Fatal(err)
		}
		assertInputBoundary(t, readHelperRecord(t, directory, "ffmpeg").Args, "wav")
	}
}

func TestHelperFailuresAreSanitizedAndPartialAnalysisIsNotSuccess(t *testing.T) {
	for _, mode := range []string{"stderr", "failure", "malformed", "partial"} {
		t.Run(mode, func(t *testing.T) {
			h, _ := testHelpers(t, mode)
			result, err := analyze(t.Context(), testFile(t, []byte("synthetic")), "mp3", h)
			if err == nil || result.Duration != 0 || len(result.Peaks) != 0 {
				t.Fatalf("failure became result: %+v %v", result, err)
			}
			if strings.Contains(err.Error(), "/private") || strings.Contains(err.Error(), "\x1b") {
				t.Fatalf("unsafe diagnostic: %v", err)
			}
		})
	}
	h, _ := testHelpers(t, "stderr")
	if err := play(t.Context(), testFile(t, []byte{0, 0}), "pcm", 0, h); !errors.Is(err, ErrPlaybackFailed) {
		t.Fatalf("zero-exit player error lost: %v", err)
	}
}

func TestMissingHelpersHaveActionableTypedErrors(t *testing.T) {
	h := helpers{find: func(string) (string, error) { return "", errors.New("private helper path") }}
	_, err := analyze(t.Context(), testFile(t, []byte("synthetic")), "mp3", h)
	var unavailable *UnavailableError
	if !errors.Is(err, ErrDecoderUnavailable) || !errors.As(err, &unavailable) || unavailable.Tool != "ffmpeg" {
		t.Fatal(err)
	}
	err = play(t.Context(), testFile(t, []byte{0, 0}), "pcm", 0, h)
	if !errors.Is(err, ErrPlayerUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	if _, err := analyze(t.Context(), testFile(t, []byte{0, 0}), "pcm", h); err != nil {
		t.Fatalf("PCM needs no helper: %v", err)
	}
	if _, err := analyze(t.Context(), testFile(t, testWave(1, 1, 16, 24000, []byte{0, 0})), "wav", h); err != nil {
		t.Fatalf("WAV needs no helper: %v", err)
	}
}

func waitForHelper(t *testing.T, directory, tool string, done <-chan error) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(directory, tool+".json")); err == nil {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("helper exited before ready: %v", err)
		case <-timer.C:
			t.Fatal("helper readiness deadline")
		case <-ticker.C:
		}
	}
}

func TestHelperCancellationWaitsForOwnedProcesses(t *testing.T) {
	for _, playback := range []bool{false, true} {
		h, directory := testHelpers(t, "hold")
		ctx, cancel := context.WithCancel(t.Context())
		file := testFile(t, []byte{0, 0})
		done := make(chan error, 1)
		go func() {
			if playback {
				done <- play(ctx, file, "pcm", 0, h)
			} else {
				_, err := analyze(ctx, file, "mp3", h)
				done <- err
			}
		}()
		tool := "ffmpeg"
		if playback {
			tool = "ffplay"
		}
		waitForHelper(t, directory, tool, done)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("owned helper was not reaped")
		}
		if _, err := file.ReadAt(make([]byte, 1), 0); err != nil {
			t.Fatal("caller snapshot closed")
		}
	}
}

func TestPlaybackOffsetsUseSampleTrimmingAndDecodedSeeking(t *testing.T) {
	pcm := bytes.Repeat([]byte{0, 128}, 24000)
	h, directory := testHelpers(t, "decode")
	if err := play(t.Context(), testFile(t, pcm), "pcm", 500*time.Millisecond, h); err != nil {
		t.Fatal(err)
	}
	record := readHelperRecord(t, directory, "ffplay")
	assertInputBoundary(t, record.Args, "s16le")
	if record.Input != hex.EncodeToString(pcm[len(pcm)/2:]) || slices.Contains(record.Args, "-ss") {
		t.Fatal("native offset did not trim exact samples")
	}
	wave := testWave(1, 1, 16, 4, []byte{0, 0, 1, 0, 2, 0, 3, 0})
	if err := play(t.Context(), testFile(t, wave), "wav", 500*time.Millisecond, h); err != nil {
		t.Fatal(err)
	}
	record = readHelperRecord(t, directory, "ffplay")
	trimmed, err := hex.DecodeString(record.Input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(trimmed[44:], wave[48:]) || binary.LittleEndian.Uint32(trimmed[40:]) != 4 {
		t.Fatalf("wrong WAV offset: %v", trimmed)
	}
	h, directory = testHelpers(t, "decode")
	if err := play(t.Context(), testFile(t, []byte("synthetic")), "opus", 1250*time.Millisecond, h); err != nil {
		t.Fatal(err)
	}
	decoder := readHelperRecord(t, directory, "ffmpeg")
	seek, input := slices.Index(decoder.Args, "-ss"), slices.Index(decoder.Args, "-i")
	if seek <= input || decoder.Args[seek+1] != "1.250000000" {
		t.Fatalf("not output-side seek: %v", decoder.Args)
	}
	assertInputBoundary(t, decoder.Args, "ogg")
	assertInputBoundary(t, readHelperRecord(t, directory, "ffplay").Args, "wav")
}

func TestEarlyPlayerFailureCancelsDecoder(t *testing.T) {
	h, _ := testHelpers(t, "player-error")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := play(ctx, testFile(t, []byte("synthetic")), "flac", 0, h)
	if !errors.Is(err, ErrPlaybackFailed) || ctx.Err() != nil {
		t.Fatalf("early player failure hung or disappeared: %v", err)
	}
}
