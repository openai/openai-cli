// Package imageopen opens validated saved images when explicitly requested.
// It never generates images, selects a remote desktop, or invokes a shell.
package imageopen

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	_ "golang.org/x/image/webp"
)

// Open validates a saved image, then requests the local default image viewer.
// Successful return means the OS accepted the request, not that a window is
// already visible. The original file is never modified.
func Open(ctx context.Context, path string) error {
	return openWith(ctx, path, CheckAvailable, launch)
}

// CheckAvailable checks OS, desktop and launcher availability without opening
// anything. It can run before generation to avoid paying for an unavailable flow.
func CheckAvailable() error {
	return checkAvailable(runtime.GOOS, os.Getenv, exec.LookPath)
}

func checkAvailable(goos string, getenv func(string) string, lookPath func(string) (string, error)) error {
	command := ""
	switch goos {
	case "darwin":
		command = "/usr/bin/open"
	case "linux":
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("opening an image requires a desktop session on this machine; open the saved file on your desktop")
		}
		command = "xdg-open"
	case "windows":
		return nil
	default:
		return fmt.Errorf("opening images in a viewer is not supported on %s; open the saved file manually", goos)
	}
	if _, err := lookPath(command); err != nil {
		return fmt.Errorf("default image viewer launcher %q is unavailable; open the saved file manually", command)
	}
	return nil
}

func openWith(ctx context.Context, path string, available func() error, launch func(context.Context, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absolute, err := validateImage(ctx, path)
	if err != nil {
		return err
	}
	if err := available(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return launch(ctx, absolute)
}

func validateImage(ctx context.Context, path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve image path %q: %w", path, fileErrorCause(err))
	}
	format := map[string]string{".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg", ".webp": "webp"}[strings.ToLower(filepath.Ext(absolute))]
	if format == "" {
		return "", errors.New("image viewer accepts PNG, JPEG, or WebP files only")
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("read image %q: %w", absolute, fileErrorCause(err))
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("image viewer requires a regular image file")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return "", fmt.Errorf("read image %q: %w", absolute, fileErrorCause(err))
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("image viewer requires a regular image file")
	}
	config, detected, err := image.DecodeConfig(contextReader{ctx, file})
	if err != nil {
		return "", fmt.Errorf("inspect saved image: %w", fileErrorCause(err))
	}
	if detected != format || config.Width <= 0 || config.Height <= 0 {
		return "", errors.New("image contents do not match the PNG, JPEG, or WebP filename extension")
	}
	return absolute, ctx.Err()
}

func fileErrorCause(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}

type contextReader struct {
	ctx  context.Context
	file *os.File
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(p)
}
