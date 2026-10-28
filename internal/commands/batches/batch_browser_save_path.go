package batches

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Suggested names identify their batch without trusting API IDs as file paths.
func batchBrowserDefaultSaveName(batchID, selection string) string {
	if selection != "input" && selection != "output" && selection != "error" {
		selection = "file"
	}
	safe := batchID != "" && len(batchID) <= 128
	for _, r := range batchID {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			safe = false
			break
		}
	}
	if !safe {
		digest := sha256.Sum256([]byte(batchID))
		batchID = "batch_" + hex.EncodeToString(digest[:])
	}
	return batchID + "-" + selection + ".jsonl"
}

// Resolve only home and working-folder references. Keep Unix symlink/..
// components intact so the displayed destination matches filesystem resolution.
// This does not probe or create folders. The existing download writer owns
// no-overwrite publication and destination cleanup.
func batchBrowserSaveDestination(value string) (string, error) {
	if strings.TrimSpace(value) == "" || value == "-" || value == "~" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("Enter a filename or file path.")
	}
	if os.IsPathSeparator(value[len(value)-1]) || filepath.Base(value) == "." || filepath.Base(value) == ".." {
		return "", errors.New("Enter a filename after the folder path.")
	}
	if strings.HasPrefix(value, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(value, "~\\")) {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return "", errors.New("Could not find your home folder. Enter an absolute file path.")
		}
		value = home + string(filepath.Separator) + filepath.FromSlash(value[2:])
	}
	if filepath.IsAbs(value) {
		return value, nil
	}
	if runtime.GOOS == "windows" && (filepath.VolumeName(value) != "" || os.IsPathSeparator(value[0])) {
		return filepath.Abs(value)
	}
	directory, err := os.Getwd()
	if err != nil {
		return "", errors.New("Could not find the current folder. Enter an absolute file path.")
	}
	if !os.IsPathSeparator(directory[len(directory)-1]) {
		directory += string(filepath.Separator)
	}
	return directory + value, nil
}
