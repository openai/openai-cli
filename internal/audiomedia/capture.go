package audiomedia

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

var (
	ErrCaptureUnavailable = errors.New("microphone capture needs FFmpeg; install FFmpeg with audio device support")
	ErrCaptureFailed      = errors.New("microphone capture failed; check microphone access, the selected device, and FFmpeg audio device support")
	ErrCaptureDevice      = errors.New("choose a microphone device name or index without line breaks")
	ErrCaptureUnsupported = errors.New("microphone capture is unavailable on this operating system")
)

const captureChunkBytes = 24000 * 2 / 5 // 200 milliseconds of mono PCM16.

// ValidateCapture checks the device selection and helper availability without
// opening a device. FFmpeg builds can omit the required platform input backend.
func ValidateCapture(device string) error {
	if _, err := captureArguments(runtime.GOOS, device); err != nil {
		return err
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return ErrCaptureUnavailable
	}
	return nil
}

// CapturePCM captures 24 kHz mono signed little-endian PCM16 through FFmpeg.
// The callback receives owned 200 ms chunks, serially, with no pending queue.
// It must return promptly or honor a caller-owned cancellation context.
// A final shorter chunk contains every complete sample received, including on
// cancellation. A graceful stop can use a separate bounded callback drain context.
// CapturePCM waits for its helper and callbacks before returning.
func CapturePCM(ctx context.Context, device string, onChunk func([]byte) error) error {
	return capturePCM(ctx, device, onChunk, runtime.GOOS, systemHelpers())
}

func captureArguments(platform, device string) ([]string, error) {
	if strings.ContainsAny(device, "\x00\r\n") {
		return nil, ErrCaptureDevice
	}
	if strings.TrimSpace(device) == "" {
		device = ""
	}
	args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error", "-threads", "1"}
	switch platform {
	case "darwin":
		if device == "" {
			device = "default"
		}
		// The empty video selector before the colon requests audio only.
		args = append(args, "-f", "avfoundation", "-i", ":"+device)
	case "linux":
		if device == "" {
			device = "default"
		}
		args = append(args, "-f", "pulse", "-i", device)
	case "windows":
		if device == "" {
			return nil, errors.New("choose a Windows microphone by its device name; FFmpeg needs an explicit microphone selection")
		}
		if strings.Contains(device, ":") {
			// DirectShow treats a colon as another device selector, including video.
			return nil, errors.New("choose a Windows microphone name without a colon; FFmpeg uses colons to separate devices")
		}
		args = append(args, "-f", "dshow", "-i", "audio="+device)
	default:
		return nil, ErrCaptureUnsupported
	}
	return append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-threads", "1", "-ac", "1", "-ar", "24000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1"), nil
}

func capturePCM(ctx context.Context, device string, onChunk func([]byte) error, platform string, h helpers) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if onChunk == nil {
		return ErrCaptureFailed
	}
	args, err := captureArguments(platform, device)
	if err != nil {
		return err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	command, err := helperCommand(work, h, "ffmpeg", args)
	if err != nil {
		return ErrCaptureUnavailable
	}
	diagnostics := &helperDiagnostics{}
	command.Stderr = diagnostics
	return runCaptureCommand(ctx, command, cancel, onChunk, diagnostics, ErrCaptureFailed)
}

func runCaptureCommand(ctx context.Context, command *exec.Cmd, cancel context.CancelFunc, onChunk func([]byte) error, diagnostics *helperDiagnostics, failure error) error {
	chunks := &captureChunks{onChunk: onChunk}
	command.Stdout = cancelWriter{destination: chunks, cancel: cancel}
	runErr := command.Run()
	stopHelperProcess(command)
	finishErr := chunks.finish()
	if err := ctx.Err(); err != nil {
		return errors.Join(err, finishErr)
	}
	if finishErr != nil {
		return finishErr
	}
	if runErr != nil || (diagnostics != nil && diagnostics.present) || chunks.samples == 0 {
		return failure
	}
	return nil
}

type captureChunks struct {
	onChunk func([]byte) error
	buffer  [captureChunkBytes]byte
	length  int
	samples int64
	err     error
}

func (c *captureChunks) Write(data []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	written := 0
	for len(data) > 0 {
		n := copy(c.buffer[c.length:], data)
		c.length += n
		written += n
		data = data[n:]
		if c.length == len(c.buffer) {
			if err := c.deliver(c.length); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (c *captureChunks) deliver(length int) error {
	chunk := append([]byte(nil), c.buffer[:length]...)
	c.length = 0
	c.samples += int64(length / 2)
	c.err = c.onChunk(chunk)
	return c.err
}

func (c *captureChunks) finish() error {
	if c.err != nil {
		return c.err
	}
	odd := c.length%2 != 0
	if complete := c.length - c.length%2; complete > 0 {
		if err := c.deliver(complete); err != nil {
			return err
		}
	}
	if odd {
		return ErrInvalidAudio
	}
	return nil
}
