package custom

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// An opened parent binds staging and publication to the same directory.
// Linking the complete staging file never replaces a concurrently created destination.
type schemaArtifact struct {
	root        *os.Root
	file        *os.File
	name, stage string
	published   bool
	identity    os.FileInfo
	parent      os.FileInfo
	directory   string
}

func prepareSchemaArtifact(path string) (*schemaArtifact, error) {
	if path == "" || path == "-" {
		return nil, &schemaHelperError{message: "Choose a new schema file with --output. Stdout destinations are unsupported. No request was sent."}
	}
	directory, name := filepath.Split(path)
	if name == "" || name == "." || name == ".." {
		return nil, &schemaHelperError{message: "Choose a new schema filename with --output, not a directory. No request was sent."}
	}
	if directory == "" {
		directory = "."
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, &schemaHelperError{message: "Could not open the schema output directory. Create it or change --output. No request was sent.", cause: err}
	}
	parent, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, &schemaHelperError{message: "Could not inspect the schema output directory. Change --output. No request was sent.", cause: err}
	}
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return nil, &schemaHelperError{message: "Schema destination exists or cannot be inspected. Choose a new --output path. No request was sent.", cause: err}
	}
	stage := ".openai-schema-" + rand.Text()
	file, err := root.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		root.Close()
		return nil, &schemaHelperError{message: "Could not stage the schema file. Check output directory permissions or change --output. No request was sent.", cause: err}
	}
	identity, err := file.Stat()
	if err != nil {
		file.Close()
		root.Close()
		return nil, &schemaHelperError{message: "Could not inspect the schema staging file. No request was sent. Inspect the output directory.", cause: err}
	}
	return &schemaArtifact{root: root, file: file, name: name, stage: stage, identity: identity, parent: parent, directory: directory}, nil
}

func (a *schemaArtifact) publish(ctx context.Context, data []byte) error {
	if err := a.checkIdentity(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.file.Truncate(0); err != nil {
		return &schemaHelperError{message: "Could not prepare the schema staging file. No destination was created. Check output permissions.", cause: err}
	}
	original := data
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := data[:min(len(data), 32*1024)]
		n, err := a.file.Write(chunk)
		if err == nil && n != len(chunk) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return &schemaHelperError{message: "Could not write the schema staging file. No destination was created. Check disk space and output permissions.", cause: err}
		}
		data = data[n:]
	}
	// Confirm that the staged snapshot still contains precisely the compiled bytes.
	buffer := make([]byte, 32*1024)
	for offset := 0; offset < len(original); {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := min(len(buffer), len(original)-offset)
		n, err := a.file.ReadAt(buffer[:size], int64(offset))
		if err != nil || n != size || !bytes.Equal(buffer[:size], original[offset:offset+size]) {
			return &schemaHelperError{message: "Schema staging content changed. No destination was created. Use an output directory without concurrent writers.", cause: err}
		}
		offset += size
	}
	info, err := a.file.Stat()
	if err != nil || info.Size() != int64(len(original)) {
		return &schemaHelperError{message: "Schema staging content changed. No destination was created. Use an output directory without concurrent writers.", cause: err}
	}
	if err := a.file.Sync(); err != nil {
		return &schemaHelperError{message: "Could not finish the schema staging file. No destination was created. Check disk space and output permissions.", cause: err}
	}
	if err := a.checkIdentity(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.root.Link(a.stage, a.name); err != nil {
		return &schemaHelperError{message: "Could not publish the schema file. Any existing destination was preserved. Choose a new --output path on a filesystem supporting hard links.", cause: err}
	}
	a.published = true
	if info, err := a.root.Lstat(a.name); err != nil || !os.SameFile(a.identity, info) {
		return &schemaHelperError{message: "Schema destination changed during publication. Inspect --output before using it or repeating generation.", cause: err}
	}
	if info, err := os.Stat(a.directory); err != nil || !os.SameFile(a.parent, info) {
		return &schemaHelperError{message: "Schema saved in the original directory, but its path changed. Locate that directory before repeating generation.", cause: err}
	}
	return nil
}

func (a *schemaArtifact) checkIdentity() error {
	info, err := a.root.Lstat(a.stage)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(a.identity, info) {
		return &schemaHelperError{message: "Schema staging file changed. No destination was created. Use an output directory without concurrent writers.", cause: err}
	}
	info, err = os.Stat(a.directory)
	if err != nil || !os.SameFile(a.parent, info) {
		return &schemaHelperError{message: "Schema output directory changed. No destination was created. Check --output before another paid request.", cause: err}
	}
	return nil
}

func (a *schemaArtifact) cleanup() error {
	var err error
	if a.file != nil {
		err = a.file.Close()
	}
	info, inspectErr := a.root.Lstat(a.stage)
	if inspectErr == nil && os.SameFile(a.identity, info) {
		err = errors.Join(err, a.root.Remove(a.stage))
	} else if !errors.Is(inspectErr, os.ErrNotExist) {
		err = errors.Join(err, errors.New("staging identity changed"))
	}
	err = errors.Join(err, a.root.Close())
	if err == nil {
		return nil
	}
	message := "Could not clean up the schema staging file. Inspect .openai-schema-* in the output directory."
	if a.published {
		message = "Schema saved. " + message
	}
	return &schemaHelperError{message: message, cause: err, saved: a.published}
}
