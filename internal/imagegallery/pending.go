package imagegallery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrPending = errors.New("a previous sharp preview may still be registered; retry that saved image with the same font settings or open a new Terminal tab")

type pendingAttempt struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Revision    string `json:"revision"`
	Typography  string `json:"typography,omitempty"`
	Registering bool   `json:"registering"`
}

func (g *Gallery) recoverPending() error {
	data, err := readPrivate(filepath.Join(g.directory, ".pending.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Lstat(filepath.Join(g.directory, ".pending")); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("image gallery has unrecognized pending storage")
	}
	if err != nil {
		return err
	}
	var attempt pendingAttempt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&attempt) != nil || decoder.Decode(&extra) != io.EOF || attempt.Version != 1 || !isHex(attempt.ID, 32) || !isHex(g.state.ID, 32) ||
		!strings.HasPrefix(attempt.Revision, "OpenAIImages-"+g.state.ID[:8]+"-") || !strings.HasSuffix(attempt.Revision, "-Regular") ||
		!isHex(strings.TrimSuffix(strings.TrimPrefix(attempt.Revision, "OpenAIImages-"+g.state.ID[:8]+"-"), "-Regular"), 32) ||
		attempt.Typography != "" && !isHex(attempt.Typography, 32) || attempt.Registering && attempt.Typography == "" {
		return errors.New("invalid pending image gallery attempt")
	}
	if err := checkPrivate(filepath.Join(g.directory, ".pending"), true); err != nil &&
		(!errors.Is(err, os.ErrNotExist) || attempt.Registering && g.state.CompletedAttempt != attempt.ID) {
		return err
	}
	g.attempt = &attempt
	if !attempt.Registering || g.state.CompletedAttempt == attempt.ID {
		return g.discardPending(g.state.CompletedAttempt == attempt.ID)
	}
	return nil
}

func (g *Gallery) reserveAttempt(ctx context.Context, revision *Revision, typography string) error {
	if g.attempt != nil && g.attempt.ID == g.state.CompletedAttempt {
		if err := g.discardPending(true); err != nil {
			return err
		}
	}
	if g.attempt != nil && g.attempt.Revision != revision.state.PostScript {
		if g.attempt.Registering {
			return ErrPending
		}
		if err := g.discardPending(false); err != nil {
			return err
		}
	}
	if g.attempt == nil {
		if _, err := os.Lstat(filepath.Join(g.directory, ".pending")); !errors.Is(err, os.ErrNotExist) {
			return errors.New("image gallery has unrecognized pending storage")
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		attempt := pendingAttempt{Version: 1, ID: id, Revision: revision.state.PostScript, Typography: typography}
		data, err := json.Marshal(attempt)
		if err != nil {
			return err
		}
		path := filepath.Join(g.directory, ".pending.json")
		if err := replaceMetadata(ctx, g.directory, ".pending.json", data); err != nil {
			return err
		}
		if err := os.Mkdir(filepath.Join(g.directory, ".pending"), 0700); err != nil {
			_ = os.Remove(path)
			return err
		}
		g.attempt = &attempt
	}
	if typography != "" && g.attempt.Typography != typography {
		if g.attempt.Registering {
			return ErrPending
		}
		// A different text face may replace a preparation that never reached
		// native registration. Keep its PNG because this revision still uses it.
		files, err := g.pendingArtifacts()
		if err != nil {
			return err
		}
		for _, file := range files {
			if isFontName(file.Name()) {
				if err := g.removePendingArtifact(file.Name(), false); err != nil {
					return err
				}
			}
		}
		next := *g.attempt
		next.Typography = typography
		if err := g.saveAttempt(ctx, next); err != nil {
			return err
		}
	}
	revision.attemptID = g.attempt.ID
	return nil
}

func (g *Gallery) saveAttempt(ctx context.Context, next pendingAttempt) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := replaceMetadata(ctx, g.directory, ".pending.json", data); err != nil {
		return err
	}
	g.attempt = &next
	return nil
}

// MarkRegistering must precede the first native registration for this revision.
// A failed reply can still leave a registered or active font, so later requests
// must retain this exact attempt until its successful commit.
func (g *Gallery) MarkRegistering(ctx context.Context, revision *Revision) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	if revision == nil || revision.owner != g || g.pending != revision {
		return errors.New("image gallery revision is stale or belongs to another gallery")
	}
	if revision.attemptID == "" {
		return nil
	}
	if g.attempt == nil || g.attempt.ID != revision.attemptID || g.attempt.Typography == "" {
		return errors.New("image gallery typography is not prepared")
	}
	if g.attempt.Registering {
		return nil
	}
	next := *g.attempt
	next.Registering = true
	return g.saveAttempt(ctx, next)
}

// Staging hardlinks prove which final files this attempt created. Existing
// immutable files are never claimed or removed, even when their bytes match.
func (g *Gallery) writeArtifact(ctx context.Context, path string, data []byte) error {
	if err := checkPrivate(filepath.Join(g.directory, ".pending"), true); err != nil {
		return err
	}
	stage := filepath.Join(g.directory, ".pending", filepath.Base(path))
	if err := writeNew(ctx, stage, data); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	staged, err := readPrivate(stage, int64(len(data)))
	if err != nil || !bytes.Equal(staged, data) {
		return errors.New("pending image gallery artifact does not match its content")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Link(stage, path)
}

func (g *Gallery) pendingArtifacts() ([]os.DirEntry, error) {
	stage := filepath.Join(g.directory, ".pending")
	if err := checkPrivate(stage, true); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	files, err := os.ReadDir(stage)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, file := range files {
		name := file.Name()
		if !isFontName(name) && !(strings.HasSuffix(name, ".png") && isHex(strings.TrimSuffix(name, ".png"), 64)) {
			return nil, errors.New("unrecognized pending image gallery artifact")
		}
		if err := checkPrivate(filepath.Join(stage, name), false); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func (g *Gallery) removePendingArtifact(name string, retainFinal bool) error {
	staged := filepath.Join(g.directory, ".pending", name)
	if !retainFinal {
		directory := "images"
		if isFontName(name) {
			directory = "fonts"
		}
		final := filepath.Join(g.directory, directory, name)
		stageInfo, err := os.Lstat(staged)
		if err != nil {
			return err
		}
		finalInfo, err := os.Lstat(final)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && os.SameFile(stageInfo, finalInfo) {
			if err := os.Remove(final); err != nil {
				return err
			}
		}
	}
	return os.Remove(staged)
}

func (g *Gallery) discardPending(retainFinal bool) error {
	files, err := g.pendingArtifacts()
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := g.removePendingArtifact(file.Name(), retainFinal); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(g.directory, ".pending")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Join(g.directory, ".pending.json")); err != nil {
		return err
	}
	g.attempt = nil
	return nil
}

func replaceMetadata(ctx context.Context, directory, destination string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".state-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(directory, destination))
}
