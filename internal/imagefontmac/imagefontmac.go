// Package imagefontmac connects opt-in image galleries to macOS's font APIs.
// Font registration does not control Terminal. Separate user-invoked operations
// update the caller's exact tab while preserving its selected profile and size.
package imagefontmac

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrUnsupported = errors.New("Apple Terminal image fonts require local macOS with /usr/bin/osascript")

// NativeError reports a CoreText error without exposing native diagnostics,
// which can contain unescaped filenames or other private image metadata.
type NativeError struct {
	Operation string
	Code      int
}

func (e *NativeError) Error() string {
	return fmt.Sprintf("macOS image-font %s failed (CoreText error %d)", e.Operation, e.Code)
}

//go:embed bridge.js
var bridge string

const interpreter = "/usr/bin/osascript"

type runner func(context.Context, string, []string, []string) ([]byte, error)

type nativeResult struct {
	OK   bool `json:"ok"`
	Code int  `json:"code"`
}

// Register makes a generated font available for this macOS login session.
// The caller must retain the file at the same path until it is unregistered.
func Register(ctx context.Context, path string) error {
	_, err := invoke(ctx, "register", path, Supported, run)
	return err
}

// Unregister removes a session registration. A font that is already absent is
// treated as successfully removed. This does not delete the caller's font file.
func Unregister(ctx context.Context, path string) error {
	_, err := invoke(ctx, "unregister", path, Supported, run)
	return err
}

func invoke(ctx context.Context, action, path string, supported func() bool, execute runner) (nativeResult, error) {
	if err := ctx.Err(); err != nil {
		return nativeResult{}, err
	}
	if !supported() {
		return nativeResult{}, ErrUnsupported
	}
	if action != "register" && action != "unregister" {
		return nativeResult{}, errors.New("invalid image-font operation")
	}
	if strings.IndexByte(path, 0) >= 0 || path == "" {
		return nativeResult{}, errors.New("provide a generated font file")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nativeResult{}, errors.New("resolve generated font path")
	}
	// Cleanup may run after a cache file was removed. CoreText can unregister
	// its original file URL without requiring the file to remain readable.
	if action != "unregister" {
		info, err := os.Stat(path)
		if err != nil {
			return nativeResult{}, fmt.Errorf("read generated font %q: %w", path, fileErrorCause(err))
		}
		if !info.Mode().IsRegular() {
			return nativeResult{}, errors.New("generated font must be a regular file")
		}
	}
	output, err := execute(ctx, interpreter, []string{"-l", "JavaScript", "-e", bridge, action, path}, environment(os.Environ()))
	if ctx.Err() != nil {
		return nativeResult{}, ctx.Err()
	}
	if err != nil {
		// An interpreter diagnostic is not part of the trusted output contract.
		// Never forward stderr, paths in exec errors, or evaluated source text.
		return nativeResult{}, fmt.Errorf("run macOS image-font %s: native bridge failed", action)
	}
	var result nativeResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nativeResult{}, errors.New("macOS image-font bridge returned an invalid result")
	}
	if !result.OK {
		return nativeResult{}, &NativeError{Operation: action, Code: result.Code}
	}
	return result, nil
}

func run(ctx context.Context, program string, args, environment []string) ([]byte, error) {
	command := exec.CommandContext(ctx, program, args...)
	command.Env = environment
	return command.Output()
}

func environment(source []string) []string {
	result := make([]string, 0, len(source))
	for _, entry := range source {
		if !strings.HasPrefix(strings.ToUpper(entry), "OPENAI_") {
			result = append(result, entry)
		}
	}
	return result
}

func fileErrorCause(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
