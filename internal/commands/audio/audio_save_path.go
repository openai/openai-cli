package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/imageoutput"
	clioutput "github.com/openai/openai-cli/internal/output"
)

const audioDefaultSaveFolder = "~/Downloads/gpt-audio"

// Suggesting a destination does not create directories or files. The retained
// request owns the name and extension, even after the user edits the form.
func audioDefaultSavePath(entry audiohistory.Entry) (string, error) {
	if !slices.Contains(audioFormats, entry.Settings.Format) {
		return "", errors.New("The retained audio format is unknown. Enter a filename.")
	}
	stem := imageoutput.NameFromPrompt(entry.Settings.Input)
	if stem == "" {
		stem = "speech"
		if id := imageoutput.NameFromPrompt(entry.ID); id != "" {
			stem += "-" + id
		}
	}
	name := stem + "." + entry.Settings.Format
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return name, errors.New("Could not find your home folder. Enter an absolute folder or filename path.")
	}
	return filepath.Join(home, "Downloads", "gpt-audio", name), nil
}

// Expand only the current user's home. Keep relative paths and parent components
// intact so explicit destinations preserve filesystem resolution and overwrite behavior.
func audioExpandSaveHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~\\") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", errors.New("Could not find your home folder. Enter an absolute folder or filename path.")
	}
	if path == "~" {
		return home, nil
	}
	return home + string(filepath.Separator) + filepath.FromSlash(path[2:]), nil
}

func audioSaveHasPath(filename string) bool {
	return strings.ContainsRune(filename, filepath.Separator) || filepath.VolumeName(filename) != "" ||
		(filepath.Separator == '\\' && strings.Contains(filename, "/")) || strings.HasPrefix(filename, "~\\")
}

func audioSaveFilenameError(filename string) error {
	if filename == "" || filename == "-" || filename == "/dev/stdout" || strings.ContainsAny(filename, "\r\n\x00") {
		return errors.New("Enter a filename for saving.")
	}
	if audioSaveHasPath(filename) {
		if os.IsPathSeparator(filename[len(filename)-1]) || filepath.Base(filename) == "." || filepath.Base(filename) == ".." {
			return errors.New("Enter a filename for saving.")
		}
		return nil
	}
	if imageoutput.ValidateName(filename) != nil {
		return errors.New("Enter a filename for saving.")
	}
	return nil
}

func audioSaveFolderError(folder string) error {
	if strings.TrimSpace(folder) == "" || strings.ContainsAny(folder, "\r\n\x00") {
		return errors.New("Enter a folder for saving.")
	}
	_, err := audioExpandSaveHome(folder)
	return err
}

// Resolve captured editor values without creating or probing files. A pasted
// path is authoritative; a bare filename uses the selected folder unchanged.
func audioSaveDestination(filename, folder string) (string, bool, error) {
	if err := audioSaveFilenameError(filename); err != nil {
		return "", false, err
	}
	if audioSaveHasPath(filename) {
		path, err := audioExpandSaveHome(filename)
		return path, false, err
	}
	if err := audioSaveFolderError(folder); err != nil {
		return "", false, err
	}
	directory, err := audioExpandSaveHome(folder)
	if err != nil {
		return "", false, err
	}
	defaultDirectory, defaultErr := audioExpandSaveHome(audioDefaultSaveFolder)
	trimSeparators := func(path string) string {
		return strings.TrimRightFunc(path, func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) })
	}
	useDefault := defaultErr == nil && trimSeparators(filepath.FromSlash(directory)) == trimSeparators(filepath.FromSlash(defaultDirectory))
	path := directory
	if !os.IsPathSeparator(path[len(path)-1]) {
		path += string(filepath.Separator)
	}
	return path + filename, useDefault, nil
}

// The default folder permits creation and numbered names, including edited names.
// Custom paths retain the existing staged export and explicit overwrite contract.
func saveWorkbenchAudio(ctx context.Context, snapshot *audiohistory.Snapshot, path string, useDefault bool) (savedPath string, err error) {
	defer func() {
		if err != nil {
			message := "Could not save audio. Check the filename and folder permissions."
			if errors.Is(err, syscall.ENAMETOOLONG) {
				message = "Could not save audio. Use a shorter filename or folder."
			}
			err = clioutput.NewDownloadSaveError(message, err)
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if useDefault && path == "" {
		path, err = audioDefaultSavePath(snapshot.Entry)
		if err != nil {
			return "", err
		}
	}
	if path == "" || path == "-" || path == "/dev/stdout" || strings.ContainsAny(path, "\r\n\x00") {
		return "", errors.New("Enter a filename for saving.")
	}
	if !useDefault {
		return path, exportAudio(ctx, snapshot, path, nil)
	}
	directory, name := filepath.Split(path)
	if directory == "" {
		directory = "."
	}
	if err := audioSaveFilenameError(name); err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for sequence := int64(1); ; sequence++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		candidate := name
		if sequence > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, sequence, extension)
		}
		file, createErr := clioutput.CreatePrivateDownloadFile(root, candidate)
		if createErr != nil {
			// Lstat also recognizes directory and symlink collisions on Windows.
			if _, statErr := root.Lstat(candidate); statErr == nil {
				if sequence == 1<<63-1 {
					return "", errors.New("No unused audio filename remains.")
				}
				continue
			}
			return "", createErr
		}
		path = filepath.Join(directory, candidate)
		if err := writeWorkbenchAudio(ctx, root, candidate, path, file, snapshot); err != nil {
			return "", err
		}
		return path, nil
	}
}

func writeWorkbenchAudio(ctx context.Context, root *os.Root, name, path string, file *os.File, snapshot *audiohistory.Snapshot) (err error) {
	owned, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	defer func() {
		if err != nil {
			current, statErr := root.Lstat(name)
			if errors.Is(statErr, os.ErrNotExist) {
				return
			}
			if statErr != nil || !os.SameFile(current, owned) {
				err = errors.Join(err, clioutput.ErrDownloadDestinationChanged, statErr)
				return
			}
			err = errors.Join(err, root.Remove(name))
		}
	}()
	reader := io.NewSectionReader(snapshot.File, 0, snapshot.Entry.Bytes)
	if err = errors.Join(clioutput.CopyDownloadData(ctx, file, reader), file.Close()); err != nil {
		return err
	}
	// The pinned root owns cleanup, but the public path owns the receipt.
	// A renamed or replaced parent must not turn a completed copy into success.
	current, statErr := root.Lstat(name)
	parent, parentErr := os.Stat(filepath.Dir(path))
	pinned, pinnedErr := root.Stat(".")
	public, publicErr := os.Lstat(path)
	if statErr != nil || parentErr != nil || pinnedErr != nil || publicErr != nil ||
		!os.SameFile(current, owned) || !os.SameFile(parent, pinned) || !os.SameFile(public, owned) {
		return errors.Join(clioutput.ErrDownloadDestinationChanged, statErr, parentErr, pinnedErr, publicErr)
	}
	return nil
}
