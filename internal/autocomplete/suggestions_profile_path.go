package autocomplete

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
)

// PathPrecedesSuggestions recognizes the installer's PATH selection immediately
// before an intact, final insertion loader. A later user instruction must win
// the normal PATH check, even if it follows an older, valid loader.
func PathPrecedesSuggestions(file *os.File, size int64, profile, line, executable string) bool {
	_, marker, _, _ := pickerIntegrationMarkers(suggestionsIntegration)
	end := []byte(marker + "\n")
	if size < int64(len(end)) || size > pickerInstallLimit {
		return false
	}
	tail := make([]byte, len(end))
	if _, err := file.ReadAt(tail, size-int64(len(tail))); err != nil || !bytes.Equal(tail, end) {
		return false
	}
	data := make([]byte, size)
	if _, err := file.ReadAt(data, 0); err != nil {
		return false
	}
	shell := CompletionStyleBash
	if filepath.Base(profile) == ".zshrc" {
		shell = CompletionStyleZsh
	}
	options := PickerInstallation{Profile: profile, Shell: shell, integration: suggestionsIntegration}
	before, after, block, err := parsePickerBlock(data, options)
	if err != nil || block == nil || block.Executable != executable || len(after) != 0 {
		return false
	}
	before = bytes.TrimSuffix(before, []byte("\n"))
	if !bytes.Equal(before, []byte(line)) && !bytes.HasSuffix(before, []byte("\n"+line)) {
		return false
	}
	options.Directory = filepath.Dir(block.Script)
	_, current, err := inspectPickerInstallation(context.Background(), options)
	return err == nil && current
}
