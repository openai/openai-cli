package audiomedia

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestCaptureFilePCMInputBoundary(t *testing.T) {
	for _, tc := range []struct{ name, demux, protocols, input string }{
		{"voice.WAV", "wav", "pipe", "pipe:0"}, {"-take.m4a", "mov", "cache,pipe", "cache:pipe:0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			data := []byte("test input")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			h, dir := testHelpers(t, "decode")
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if runtime.GOOS != "windows" {
				// A replacement during session setup cannot change the selected input.
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var received int
			err = captureFilePCM(t.Context(), file, func(chunk []byte) error {
				received += len(chunk)
				return nil
			}, h)
			if err != nil || received == 0 || received%2 != 0 {
				t.Fatalf("decoded %d PCM bytes: %v", received, err)
			}
			r := readHelperRecord(t, dir, "ffmpeg")
			if r.Input != hex.EncodeToString(data) || slices.Contains(r.Args, path) {
				t.Fatal("file did not reach decoder exclusively through stdin")
			}
			for flag, want := range map[string]string{"-i": tc.input, "-protocol_whitelist": tc.protocols, "-f": tc.demux, "-ac": "1", "-ar": "24000"} {
				i := slices.Index(r.Args, flag)
				if i < 0 || i+1 >= len(r.Args) || r.Args[i+1] != want {
					t.Fatalf("expected %s %s in %v", flag, want, r.Args)
				}
			}
		})
	}
	if _, err := OpenCaptureFile("https://example.invalid/recording.wav"); !errors.Is(err, errCaptureFileInput) {
		t.Fatalf("URL: %v", err)
	}
	if _, err := OpenCaptureFile("playlist.m3u8"); !errors.Is(err, errCaptureFileFormat) {
		t.Fatalf("playlist: %v", err)
	}
}

func TestCaptureInstalledAudioFile(t *testing.T) {
	if os.Getenv("OPENAI_CLI_AUDIO_NATIVE_TEST") != "1" {
		t.Skip("set OPENAI_CLI_AUDIO_NATIVE_TEST=1 to use the installed FFmpeg with synthetic files")
	}
	dir := t.TempDir()
	pcm := bytes.Repeat([]byte{0, 0, 0x30, 0x75, 0, 0, 0xd0, 0x8a}, 2100)
	wav := testWave(1, 1, 16, 24000, pcm)
	source := filepath.Join(dir, "input.wav")
	if err := os.WriteFile(source, wav, 0600); err != nil {
		t.Fatal(err)
	}
	translate := func(ctx context.Context, path string) ([]byte, error) {
		var decoded bytes.Buffer
		file, err := OpenCaptureFile(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		err = CaptureFilePCM(ctx, file, func(chunk []byte) error {
			_, err := decoded.Write(chunk)
			return err
		})
		return decoded.Bytes(), err
	}
	got, err := translate(t.Context(), source)
	if err != nil || !bytes.Equal(got, pcm) {
		t.Fatalf("WAV decoded %d bytes, want %d identical bytes: %v", len(got), len(pcm), err)
	}
	// The default M4A muxer writes its index after the media data.
	m4a := filepath.Join(dir, "voice.m4a")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "error", "-i", source, "-c:a", "aac", m4a)
	cmd.Env = helperEnvironment()
	if err := cmd.Run(); err != nil {
		t.Fatalf("synthetic M4A encoding: %v", err)
	}
	got, err = translate(t.Context(), m4a)
	if err != nil || len(got) < len(pcm)/2 || bytes.Equal(got, make([]byte, len(got))) {
		t.Fatalf("M4A decoded %d PCM bytes: %v", len(got), err)
	}
	renamed := filepath.Join(dir, "playlist.wav")
	if err := os.WriteFile(renamed, []byte("ffconcat version 1.0\nfile 'input.wav'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = translate(t.Context(), renamed)
	if !errors.Is(err, errCaptureFileDecode) || len(got) != 0 {
		t.Fatalf("renamed playlist produced %d samples: %v", len(got)/2, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := translate(ctx, source); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
}
