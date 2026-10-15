package audiomedia

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type helpers struct {
	find    func(string) (string, error)
	command func(context.Context, string, ...string) *exec.Cmd
}

func systemHelpers() helpers { return helpers{exec.LookPath, exec.CommandContext} }

func demuxer(format string) (string, error) {
	switch format {
	case "mp3", "aac", "flac", "wav":
		return format, nil
	case "opus":
		return "ogg", nil
	case "pcm":
		return "s16le", nil
	default:
		return "", ErrUnsupportedFormat
	}
}

func helperCommand(ctx context.Context, h helpers, name string, args []string) (*exec.Cmd, error) {
	path, err := h.find(name)
	if err != nil {
		return nil, &UnavailableError{Tool: name}
	}
	command := h.command(ctx, path, args...)
	command.Env = helperEnvironment()
	command.WaitDelay = 500 * time.Millisecond
	configureHelperProcess(command)
	return command, nil
}

// Helpers need local device/session settings, not API credentials or FFREPORT.
// FFREPORT can create unredacted log files even when stderr is discarded.
func helperEnvironment() []string {
	var result []string
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "HOME",
		"LANG", "LC_ALL", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "PULSE_SERVER",
		"SDL_AUDIODRIVER", "SDL_VIDEODRIVER", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return append(result, "AV_LOG_FORCE_NOCOLOR=1")
}

// Error output is consumed without retaining a single byte. FFplay can report
// device/input failure at loglevel error and still exit zero, so presence matters.
type helperDiagnostics struct{ present bool }

func (d *helperDiagnostics) Write(p []byte) (int, error) {
	if len(p) > 0 {
		d.present = true
	}
	return len(p), nil
}

func decodeArguments(format string, offset time.Duration) ([]string, error) {
	demux, err := demuxer(format)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error", "-threads", "1",
		"-protocol_whitelist", "pipe", "-f", demux}
	if format == "pcm" {
		// Raw PCM defaults to mono across old and current FFmpeg releases.
		// The channel-option name changed; the sample-rate option is stable.
		args = append(args, "-sample_rate", "24000")
	}
	args = append(args, "-i", "pipe:0")
	// Output-side seeking decodes/discards preceding samples, unlike a nearest
	// compressed input seek. The player receives an already-trimmed WAV stream.
	if offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 9, 64))
	}
	return append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-threads", "1",
		"-c:a", "pcm_f32le", "-f", "wav", "pipe:1"), nil
}

func playerArguments(format string) ([]string, error) {
	demux, err := demuxer(format)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-nodisp", "-autoexit", "-vn", "-sn", "-nostats", "-loglevel", "error",
		"-protocol_whitelist", "pipe", "-f", demux}
	if format == "pcm" {
		args = append(args, "-sample_rate", "24000")
	}
	return append(args, "-i", "pipe:0"), nil
}

type cancelWriter struct {
	destination io.Writer
	cancel      context.CancelFunc
}

func (w cancelWriter) Write(data []byte) (int, error) {
	n, err := w.destination.Write(data)
	if err != nil {
		w.cancel()
	}
	return n, err
}

func decodeAnalysis(ctx context.Context, file *os.File, size int64, format string, h helpers) (*pcmAnalyzer, error) {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	args, err := decodeArguments(format, 0)
	if err != nil {
		return nil, err
	}
	command, err := helperCommand(work, h, "ffmpeg", args)
	if err != nil {
		return nil, err
	}
	decoded := &decodedWave{ctx: work}
	diagnostics := &helperDiagnostics{}
	command.Stdin = &contextReader{work, io.NewSectionReader(file, 0, size)}
	command.Stdout = cancelWriter{decoded, cancel}
	command.Stderr = diagnostics
	runErr := command.Run()
	stopHelperProcess(command)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if decoded.err != nil {
		return nil, ErrInvalidAudio
	}
	if runErr != nil || diagnostics.present {
		return nil, ErrAnalysisFailed
	}
	if err := decoded.finish(); err != nil {
		return nil, ErrInvalidAudio
	}
	return decoded.analyzer, nil
}

func playReader(ctx context.Context, source io.Reader, format string, h helpers) error {
	args, err := playerArguments(format)
	if err != nil {
		return err
	}
	command, err := helperCommand(ctx, h, "ffplay", args)
	if err != nil {
		return err
	}
	diagnostics := &helperDiagnostics{}
	command.Stdin, command.Stdout, command.Stderr = &contextReader{ctx, source}, io.Discard, diagnostics
	err = command.Run()
	stopHelperProcess(command)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err != nil || diagnostics.present {
		return ErrPlaybackFailed
	}
	return nil
}

func playDecoded(ctx context.Context, file *os.File, size int64, format string, offset time.Duration, h helpers) error {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	decodeArgs, err := decodeArguments(format, offset)
	if err != nil {
		return err
	}
	decoder, err := helperCommand(work, h, "ffmpeg", decodeArgs)
	if err != nil {
		return err
	}
	playArgs, _ := playerArguments("wav")
	player, err := helperCommand(work, h, "ffplay", playArgs)
	if err != nil {
		return err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return ErrPlaybackFailed
	}
	defer read.Close()
	defer write.Close()
	decoderDiagnostics, playerDiagnostics := &helperDiagnostics{}, &helperDiagnostics{}
	decoder.Stdin = &contextReader{work, io.NewSectionReader(file, 0, size)}
	decoder.Stdout, decoder.Stderr = write, decoderDiagnostics
	player.Stdin, player.Stdout, player.Stderr = read, io.Discard, playerDiagnostics
	if err := player.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrPlaybackFailed
	}
	read.Close()
	if err := decoder.Start(); err != nil {
		cancel()
		write.Close()
		_ = player.Wait()
		stopHelperProcess(player)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrPlaybackFailed
	}
	write.Close()
	type completion struct {
		player bool
		failed bool
	}
	completed := make(chan completion, 2)
	go func() {
		err := decoder.Wait()
		stopHelperProcess(decoder)
		completed <- completion{failed: err != nil || decoderDiagnostics.present}
	}()
	go func() {
		err := player.Wait()
		stopHelperProcess(player)
		completed <- completion{player: true, failed: err != nil || playerDiagnostics.present}
	}()
	first := <-completed
	var second completion
	if first.failed {
		cancel()
		second = <-completed
	} else if first.player {
		// A successful player normally observes decoder EOF just before decoder
		// shutdown. Allow that completion, but bound an orphaned decoder wait.
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case second = <-completed:
		case <-timer.C:
			cancel()
			second = <-completed
		}
		timer.Stop()
	} else {
		second = <-completed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if first.failed || second.failed {
		return ErrPlaybackFailed
	}
	return nil
}
