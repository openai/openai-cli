package audiomedia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func playerClock(seconds string) string {
	return "  " + seconds + " M-A:  0.000 fd=   0 aq=    0KB vq=    0KB sq=    0B \r"
}

func TestPlayerProgressUsesCompleteAudioClockRecords(t *testing.T) {
	var positions []time.Duration
	output := playerOutput{ctx: t.Context(), offset: 1250 * time.Millisecond,
		report: func(position time.Duration) { positions = append(positions, position) }}
	input := "    nan    :    nan fd=   0 aq=    0KB vq=    0KB sq=    0B \r" +
		playerClock("nan") + playerClock("-nan") + playerClock("inf") + playerClock("-0.10") +
		strings.Replace(playerClock("0.00"), "B \r", "B f=0/0   \r\n", 1) +
		playerClock("0.23") + playerClock("0.23") + playerClock("0.10") + playerClock("0.50")
	// Separate writes split every field and delimiter. No report uses elapsed
	// wall time or decoder byte counts.
	for _, value := range []byte(input) {
		if _, err := output.Write([]byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	output.finish()
	want := []time.Duration{1250 * time.Millisecond, 1480 * time.Millisecond, 1750 * time.Millisecond}
	if output.present || !slices.Equal(positions, want) {
		t.Fatalf("positions=%v diagnostics=%v", positions, output.present)
	}
	if output.length != 0 || output.record != [512]byte{} {
		t.Fatal("helper output remains after completion")
	}
}

func TestPlayerProgressPreservesDiagnostics(t *testing.T) {
	for _, diagnostic := range []string{
		"private device failed\n", "\n", " \r", "\x1b[31mprivate path\r",
		strings.Repeat("private", 10000) + "\r", strings.TrimSuffix(playerClock("1.00"), "\r"),
		strings.Replace(playerClock("1.00"), "M-A", "bad", 1),
		playerClock("1.00") + "private device failed",
		"private failure " + playerClock("1.00"),
	} {
		output := playerOutput{ctx: t.Context(), report: func(time.Duration) {}}
		_, _ = output.Write([]byte(diagnostic))
		output.finish()
		if !output.present || output.length != 0 || output.record != [512]byte{} {
			t.Fatal("diagnostic disappeared or remained buffered")
		}
	}
	// Play retains its original rule: any stderr bytes signal a failure.
	output := playerOutput{}
	_, _ = output.Write([]byte(playerClock("1.00")))
	output.finish()
	if !output.present {
		t.Fatal("ordinary Play now accepts stderr")
	}
}

func TestPlayerProgressRejectsCanceledUnknownAndOverflowClocks(t *testing.T) {
	for _, tc := range []struct {
		name, record string
		offset       time.Duration
		cancel       bool
	}{
		{name: "canceled", record: playerClock("1.00"), cancel: true},
		{name: "clock overflow", record: playerClock("9223372036.86")},
		{name: "offset overflow", record: playerClock("1.00"), offset: time.Duration(math.MaxInt64)},
		{name: "video clock", record: strings.Replace(playerClock("1.00"), "M-A", "M-V", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			output := playerOutput{ctx: ctx, offset: tc.offset,
				report: func(time.Duration) { t.Error("invalid or canceled clock reached caller") }}
			_, _ = output.Write([]byte(tc.record))
			output.finish()
			if output.present {
				t.Fatal("recognized status became a diagnostic")
			}
		})
	}
}

func progressHelpers(t *testing.T, mode string) helpers {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return helpers{
		find: func(name string) (string, error) { return name, nil },
		command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			command := []string{"-test.run=^TestAudioProgressHelperProcess$", "--", mode, name}
			return exec.CommandContext(ctx, binary, append(command, args...)...)
		},
	}
}

// The subprocess uses synthetic status and PCM bytes. It never opens a device.
func TestAudioProgressHelperProcess(t *testing.T) {
	index := slices.Index(os.Args, "--")
	if index < 0 {
		return
	}
	args := os.Args[index+1:]
	mode, tool := args[0], args[1]
	if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("FFREPORT") != "" {
		os.Exit(20)
	}
	if mode == "player-error" && tool == "ffplay" {
		fmt.Fprint(os.Stderr, playerClock("0.00"), "private device failed\n")
		os.Exit(0)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		os.Exit(21)
	}
	if tool == "ffmpeg" {
		if mode == "player-error" {
			for {
				time.Sleep(time.Hour)
			}
		}
		if _, err := os.Stdout.Write(testWave(1, 1, 16, 24000, []byte{0, 0})); err != nil {
			os.Exit(22)
		}
		os.Exit(0)
	}
	progress := slices.Contains(args[2:], "-stats")
	if progress == slices.Contains(args[2:], "-nostats") {
		os.Exit(23)
	}
	if mode == "quiet" || !progress {
		os.Exit(0)
	}
	if _, err := fmt.Fprint(os.Stderr, playerClock("0.00"), playerClock("0.20")); err != nil {
		os.Exit(24)
	}
	if mode == "error" {
		fmt.Fprint(os.Stderr, "private device failed\n")
	}
	if mode == "hold" {
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}

func TestPlaybackProgressPreservesOffsetAndHelperBoundaries(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-must-not-reach-helper")
	t.Setenv("FFREPORT", "synthetic-must-not-reach-helper")
	for _, tc := range []struct {
		format string
		body   []byte
		offset time.Duration
		start  time.Duration
	}{
		{format: "pcm", body: bytes.Repeat([]byte{0, 0}, 24000), offset: 500*time.Millisecond + time.Nanosecond,
			start: 500 * time.Millisecond},
		{format: "wav", body: testWave(1, 1, 16, 4, []byte{0, 0, 1, 0, 2, 0, 3, 0}),
			offset: 600 * time.Millisecond, start: 500 * time.Millisecond},
		{format: "mp3", body: []byte("synthetic compressed audio"),
			offset: 1250 * time.Millisecond, start: 1250 * time.Millisecond},
	} {
		t.Run(tc.format, func(t *testing.T) {
			var positions []time.Duration
			file := testFile(t, tc.body)
			if _, err := file.Seek(1, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			err := playWithProgress(t.Context(), file, tc.format, tc.offset,
				func(position time.Duration) { positions = append(positions, position) }, progressHelpers(t, "progress"))
			if err != nil || !slices.Equal(positions, []time.Duration{tc.start, tc.start + 200*time.Millisecond}) {
				t.Fatalf("positions=%v error=%v", positions, err)
			}
			if position, err := file.Seek(0, io.SeekCurrent); err != nil || position != 1 {
				t.Fatalf("borrowed file changed: position=%d error=%v", position, err)
			}
		})
	}
}

func TestPlaybackProgressFallbackAndZeroExitFailure(t *testing.T) {
	for _, format := range []string{"pcm", "mp3"} {
		for _, mode := range []string{"quiet", "error"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				var positions []time.Duration
				err := playWithProgress(t.Context(), testFile(t, []byte{0, 0}), format, 0,
					func(position time.Duration) { positions = append(positions, position) }, progressHelpers(t, mode))
				if mode == "quiet" && (err != nil || len(positions) != 0) {
					t.Fatalf("fallback fabricated progress or failed: %v %v", positions, err)
				}
				if mode == "error" && !errors.Is(err, ErrPlaybackFailed) {
					t.Fatalf("zero-exit failure disappeared: %v", err)
				}
			})
		}
	}
	if err := playWithProgress(t.Context(), testFile(t, []byte{0, 0}), "pcm", 0, nil, progressHelpers(t, "progress")); err != nil {
		t.Fatalf("nil callback changed ordinary playback: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := playWithProgress(ctx, testFile(t, []byte{0, 0}), "mp3", 0, func(time.Duration) {}, progressHelpers(t, "player-error"))
	if !errors.Is(err, ErrPlaybackFailed) || ctx.Err() != nil {
		t.Fatalf("early player error did not stop the decoder: %v", err)
	}
}

func TestPlaybackProgressCancellationStopsCallbacksBeforeReturn(t *testing.T) {
	for _, format := range []string{"pcm", "mp3"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first := make(chan time.Duration, 1)
			var positions []time.Duration
			done := make(chan error, 1)
			finished := make(chan struct{})
			file := testFile(t, []byte{0, 0})
			h := progressHelpers(t, "hold")
			go func() {
				defer close(finished)
				done <- playWithProgress(ctx, file, format, 0, func(position time.Duration) {
					positions = append(positions, position)
					cancel()
					first <- position
				}, h)
			}()
			defer func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("playback did not finish during test cleanup")
				}
			}()
			select {
			case <-first:
			case <-time.After(5 * time.Second):
				t.Fatal("player did not report before completion")
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || len(positions) != 1 {
					t.Fatalf("cancellation=%v positions=%v", err, positions)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled player did not return")
			}
		})
	}
}

// Run only with a reserved native-helper allocation. Both SDL drivers use dummy
// devices. This validates status syntax, not audible playback or device latency.
func TestPlayInstalledFFplayProgressDummyDevice(t *testing.T) {
	if os.Getenv("OPENAI_CLI_AUDIO_NATIVE_TEST") != "1" {
		t.Skip("requires reserved installed FFplay execution")
	}
	if _, err := exec.LookPath("ffplay"); err != nil {
		t.Fatal("the reserved native test requires installed FFplay")
	}
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	pcm := make([]byte, 24000*2)
	for _, format := range []string{"pcm", "wav"} {
		t.Run(format, func(t *testing.T) {
			body := pcm
			if format == "wav" {
				body = testWave(1, 1, 16, 24000, pcm)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var positions []time.Duration
			err := PlayWithProgress(ctx, testFile(t, body), format, 100*time.Millisecond,
				func(position time.Duration) { positions = append(positions, position) })
			if err != nil {
				t.Fatal(err)
			}
			if len(positions) < 2 || positions[0] < 100*time.Millisecond || positions[len(positions)-1] <= positions[0] {
				t.Fatalf("no advancing player clock: %v", positions)
			}
		})
	}
}
