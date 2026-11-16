package audiomedia

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

var (
	errCaptureFileInput   = errors.New("choose a readable, nonempty local audio file")
	errCaptureFileFormat  = errors.New("choose a WAV, MP3, M4A, MP4, AAC, FLAC, Ogg, Opus, AIFF, WebM, or PCM file")
	errCaptureFileDecoder = errors.New("file translation requires FFmpeg on PATH")
	errCaptureFileDecode  = errors.New("could not decode the selected file; check its audio format")
)

// Force a known demuxer, so a renamed playlist cannot select a different input.
func captureFileDemuxer(path string) (string, error) {
	if strings.Contains(path, "://") || (runtime.GOOS == "windows" && strings.HasPrefix(filepath.FromSlash(path), `\\`)) {
		return "", errCaptureFileInput
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".wav", ".mp3", ".aac", ".flac", ".opus", ".pcm":
		return demuxer(ext[1:])
	case ".ogg", ".oga":
		return "ogg", nil
	case ".aif", ".aiff":
		return "aiff", nil
	case ".mp4", ".m4a", ".mov":
		return "mov", nil
	case ".webm", ".mka", ".mkv":
		return "matroska", nil
	default:
		return "", errCaptureFileFormat
	}
}

// OpenCaptureFile pins the selected recording before the session connects.
// The caller closes the returned file when translation ends.
func OpenCaptureFile(path string) (*os.File, error) {
	if _, err := captureFileDemuxer(path); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, errCaptureFileDecoder
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errCaptureFileInput
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		file.Close()
		return nil, errCaptureFileInput
	}
	return file, nil
}

// CaptureFilePCM sends decoded mono PCM16 at playback speed. The selected file
// reaches FFmpeg through stdin, without exposing a pathname or enabling other locations.
func CaptureFilePCM(ctx context.Context, file *os.File, onChunk func([]byte) error) error {
	return captureFilePCM(ctx, file, onChunk, systemHelpers())
}

func captureFilePCM(ctx context.Context, file *os.File, onChunk func([]byte) error, h helpers) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if onChunk == nil || file == nil {
		return errCaptureFileDecode
	}
	demux, err := captureFileDemuxer(file.Name())
	if err != nil {
		return err
	}
	input, protocols := "pipe:0", "pipe"
	args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error", "-xerror", "-threads", "1", "-re", "-f", demux}
	switch demux {
	case "mov":
		// A trailing MP4 index requires seeking backwards. The cache wraps
		// only stdin. FFmpeg cannot open media URLs or referenced local files.
		input, protocols = "cache:pipe:0", "cache,pipe"
		args = append(args, "-read_ahead_limit", "-1")
	case "s16le":
		args = append(args, "-sample_rate", "24000")
	}
	args = append(args, "-protocol_whitelist", protocols, "-i", input, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-threads", "1", "-ac", "1", "-ar", "24000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	command, err := helperCommand(work, h, "ffmpeg", args)
	if err != nil {
		return errCaptureFileDecoder
	}
	command.Stdin = file
	// -xerror makes decode failures fatal. Some FFmpeg versions log a
	// recoverable cache seek at error level while successfully decoding M4A.
	return runCaptureCommand(ctx, command, cancel, onChunk, nil, errCaptureFileDecode)
}
