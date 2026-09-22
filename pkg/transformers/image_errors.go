package transformers

import (
	"errors"
	"os"
	"strings"
)

// fontPreviewDiagnostic preserves actionable preview advice without exposing
// filenames from filesystem errors, including errors wrapped by cache helpers.
func fontPreviewDiagnostic(err error) string {
	// Keep independent recovery advice (such as a failed font restoration) when
	// another joined error contains a private path.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		messages := make([]string, 0, len(joined.Unwrap()))
		for _, cause := range joined.Unwrap() {
			messages = append(messages, fontPreviewDiagnostic(cause))
		}
		return strings.Join(messages, "; ")
	}
	var pathErr *os.PathError
	var linkErr *os.LinkError
	if errors.As(err, &pathErr) || errors.As(err, &linkErr) {
		// Unwrap first so joined rollback errors retain their recovery advice.
		if cause := errors.Unwrap(err); cause != nil && pathErr != err && linkErr != err {
			return fontPreviewDiagnostic(cause)
		}
		switch {
		case errors.Is(err, os.ErrPermission):
			return "image cache access denied; check its permissions"
		case errors.Is(err, os.ErrNotExist):
			return "an image cache file is missing; retry the command"
		default:
			return "could not access image cache files"
		}
	}
	return err.Error()
}
