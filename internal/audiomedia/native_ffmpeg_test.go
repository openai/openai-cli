package audiomedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Opt in only after reserving native helper execution. This generates synthetic
// local fixtures; it never calls the API, plays sound, or installs a dependency.
func TestAnalyzeInstalledFFmpegFormats(t *testing.T) {
	if os.Getenv("OPENAI_CLI_AUDIO_NATIVE_TEST") != "1" {
		t.Skip("set OPENAI_CLI_AUDIO_NATIVE_TEST=1 after reserving installed FFmpeg execution")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("the reserved native test requires installed FFmpeg")
	}
	const rate = 24000
	data := make([]byte, 4800*2)
	for index := range 4800 {
		value := int16(18000 * math.Sin(2*math.Pi*440*float64(index)/rate))
		binary.LittleEndian.PutUint16(data[index*2:], uint16(value))
	}
	source := testWave(1, 1, 16, rate, data)
	for _, fixture := range []struct{ format, codec, container string }{
		{"mp3", "libmp3lame", "mp3"}, {"opus", "libopus", "ogg"},
		{"aac", "aac", "adts"}, {"flac", "flac", "flac"},
		{"wav", "pcm_s16le", "wav"}, {"pcm", "pcm_s16le", "s16le"},
	} {
		t.Run(fixture.format, func(t *testing.T) {
			args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error", "-threads", "1",
				"-protocol_whitelist", "pipe", "-f", "wav", "-i", "pipe:0", "-map", "0:a:0",
				"-c:a", fixture.codec, "-f", fixture.container, "pipe:1"}
			command := exec.CommandContext(t.Context(), ffmpeg, args...)
			command.Env, command.Stdin = helperEnvironment(), bytes.NewReader(source)
			encoded, err := command.Output()
			if err != nil {
				t.Fatalf("synthetic %s encoding failed", fixture.format)
			}
			result, err := Analyze(t.Context(), testFile(t, encoded), fixture.format)
			if err != nil {
				t.Fatal(err)
			}
			if result.Bytes != int64(len(encoded)) || result.Duration <= 0 || len(result.Peaks) == 0 {
				t.Fatalf("missing analysis: %+v", result)
			}
			peak := 0.0
			for _, sample := range result.Peaks {
				peak = math.Max(peak, sample)
			}
			if peak < 0.1 || peak > 1 {
				t.Fatalf("missing measured waveform: %f", peak)
			}
			// Independently count decoded sample bytes to verify duration, including
			// codec delay/padding. Resampling here also handles Opus's 48 kHz output.
			demux, _ := demuxer(fixture.format)
			args = []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", demux}
			if fixture.format == "pcm" {
				args = append(args, "-sample_rate", "24000")
			}
			args = append(args, "-i", "pipe:0", "-map", "0:a:0", "-ar", "24000", "-ac", "1", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
			command = exec.CommandContext(t.Context(), ffmpeg, args...)
			command.Env, command.Stdin = helperEnvironment(), bytes.NewReader(encoded)
			decoded, err := command.Output()
			if err != nil {
				t.Fatalf("synthetic %s reference decoding failed", fixture.format)
			}
			expected := float64(len(decoded)) / 4 / rate
			if math.Abs(result.Duration-expected) > 1.0/rate {
				t.Fatalf("decoded duration %.9f, expected %.9f", result.Duration, expected)
			}
		})
	}
}

func TestPlayInstalledFFplayDummyDevice(t *testing.T) {
	if os.Getenv("OPENAI_CLI_AUDIO_NATIVE_TEST") != "1" {
		t.Skip("requires reserved installed FFplay execution")
	}
	if _, err := exec.LookPath("ffplay"); err != nil {
		t.Fatal("the reserved native test requires installed FFplay")
	}
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	pcm := make([]byte, 2400*2)
	for _, format := range []string{"wav", "pcm"} {
		t.Run(format, func(t *testing.T) {
			data := pcm
			if format == "wav" {
				data = testWave(1, 1, 16, 24000, pcm)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := Play(ctx, testFile(t, data), format, 0); err != nil {
				t.Fatal(err)
			}
		})
	}
}
