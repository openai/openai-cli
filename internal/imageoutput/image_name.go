package imageoutput

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The allocator and preflight share a bound so every numbered filename fits
// wherever preflight succeeds, without shortening the user's chosen stem.
const maxImageSequence int64 = 1<<63 - 1

// NormalizeName strips one familiar image suffix. The response's actual format
// supplies the extension when saving; names may not contain path components.
func NormalizeName(name string) (string, error) {
	extension := filepath.Ext(name)
	switch strings.ToLower(extension) {
	case ".png", ".jpeg", ".jpg", ".webp":
		name = strings.TrimSuffix(name, extension)
	}
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

// ValidateName checks a filename stem against supported platforms' restrictions.
func ValidateName(name string) error {
	if name == "" || strings.TrimSpace(name) == "" {
		return errors.New("image name must not be empty")
	}
	if !utf8.ValidString(name) || strings.ContainsAny(name, `/\\<>:"|?*`) || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("image name must be a filename, without path separators, control characters, or <>:\"|?*")
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return errors.New("image name must not end with a space or period")
	}
	// Windows device names stay reserved with an extension or superscript digit.
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return errors.New("image name is reserved by the operating system")
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		if suffix := []rune(base[3:]); len(suffix) == 1 && strings.ContainsRune("123456789¹²³", suffix[0]) {
			return errors.New("image name is reserved by the operating system")
		}
	}
	return nil
}

// CheckName probes actual filesystem limits before requesting images. The
// directory must already exist and the name must already be normalized. Empty
// names use the short timestamp fallback and do not need a separate probe.
func CheckName(ctx context.Context, directory, normalizedName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if normalizedName == "" {
		return nil
	}
	if err := ValidateName(normalizedName); err != nil {
		return err
	}
	// Reserve the longest suffix and extension against the actual filesystem's
	// limits, including its encoding and full-path rules. An existing probe
	// already proves the name fits; never modify it or add a second suffix.
	path := filepath.Join(directory, normalizedName+"-"+strconv.FormatInt(maxImageSequence, 10)+".jpeg")
	probe, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("cannot use this image name in the output folder; try a shorter name or another folder: %w", err)
	}
	return errors.Join(removeProbe(probe), ctx.Err())
}
