// Package imagegallery stores private, immutable font revisions for terminal
// image scrollback. It never registers fonts or controls the terminal.
package imagegallery

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/openai/openai-cli/internal/imagefont"
	"golang.org/x/image/draw"
)

var (
	ErrBusy = errors.New("image gallery is already in use; finish the other image command first")
	ErrFull = errors.New("image gallery is full; open a new Terminal tab to start a new image gallery")
)

// State is a copy of the currently committed gallery metadata.
type State struct {
	Initialized                                   bool
	ID, ProfileName, FontPath, PostScript, Family string
	Revision, ImageCount, UsedGlyphs, MaxColumns  int
}

type entry struct {
	Hash    string `json:"hash"`
	Columns int    `json:"columns"`
	Rows    int    `json:"rows"`
	Start   rune   `json:"start"`
}
type diskState struct {
	Version    int     `json:"version"`
	ID         string  `json:"id"`
	Revision   int     `json:"revision"`
	Font       string  `json:"font"`
	PostScript string  `json:"postscript"`
	Family     string  `json:"family"`
	Images     []entry `json:"images"`
}

// Revision is prepared before its font is registered and activated. Commit it
// only after those operations succeed. Font files survive failed activation.
type Revision struct {
	FontPath, PostScript, Family, Text string
	ProfileName                        string
	Columns, Rows                      int
	Existing                           bool
	state                              diskState
	owner                              *Gallery
}

// Gallery holds an exclusive filesystem lock until Close. It is not safe for
// concurrent method calls. The caller must close it on every exit path.
type Gallery struct {
	directory string
	lock      *os.File
	state     diskState
	pending   *Revision
	closed    bool
}

func Open(ctx context.Context, directory string) (*Gallery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err = privateDirectory(absolute); err != nil {
		return nil, err
	}
	lock, err := acquireLock(filepath.Join(absolute, ".lock"))
	if err != nil {
		return nil, err
	}
	g := &Gallery{directory: absolute, lock: lock}
	ok := false
	defer func() {
		if !ok {
			_ = g.Close()
		}
	}()
	for _, name := range []string{"fonts", "images"} {
		if err = privateDirectory(filepath.Join(absolute, name)); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(absolute, "state.json")
	data, err := readPrivate(path, 1<<20)
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&g.state); err != nil {
			return nil, fmt.Errorf("invalid image gallery state: %w", err)
		}
		var extra any
		if err = decoder.Decode(&extra); err != io.EOF {
			return nil, errors.New("invalid image gallery state: trailing data")
		}
		if err = g.validate(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err = ctx.Err(); err != nil {
		return nil, err
	}
	ok = true
	return g, nil
}

func (g *Gallery) Close() error {
	if g.closed {
		return nil
	}
	g.closed = true
	return errors.Join(unlockFile(g.lock), g.lock.Close())
}

func (g *Gallery) State() State {
	if g.state.ID == "" {
		return State{}
	}
	used, columns := 0, 0
	for _, image := range g.state.Images {
		used += image.Columns * image.Rows
		columns = max(columns, image.Columns)
	}
	return State{Initialized: true, ID: g.state.ID, ProfileName: "OpenAI Images " + g.state.ID[:8], FontPath: filepath.Join(g.directory, "fonts", g.state.Font), PostScript: g.state.PostScript, Family: g.state.Family, Revision: g.state.Revision, ImageCount: len(g.state.Images), UsedGlyphs: used, MaxColumns: columns}
}

// Initialize prepares a base font without changing committed metadata.
func (g *Gallery) Initialize(ctx context.Context) (*Revision, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	if g.state.ID != "" {
		return g.existing(nil), nil
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	return g.build(ctx, diskState{Version: 1, ID: id, Images: []entry{}}, nil, nil)
}

// Prepare caches a reduced PNG and builds a new immutable cumulative font.
// Existing placements retain their codepoints. Showing the same image at a
// different width allocates a new placement while reusing its cached PNG.
// The source image is not modified.
func (g *Gallery) Prepare(ctx context.Context, img image.Image, columns int) (*Revision, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	if g.state.ID == "" {
		return nil, errors.New("initialize and commit the image gallery before adding images")
	}
	if columns == 0 {
		columns = 32
	}
	if columns < 1 || columns > 64 {
		return nil, errors.New("image gallery requires 1 to 64 columns")
	}
	normalized, data, err := normalize(ctx, img)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	for i := range g.state.Images {
		if g.state.Images[i].Hash == hash && g.state.Images[i].Columns == columns {
			return g.existing(&g.state.Images[i]), nil
		}
	}
	rows := min(32, max(1, int(math.Ceil(float64(columns)*float64(normalized.Bounds().Dy())/(2*float64(normalized.Bounds().Dx()))))))
	used := g.State().UsedGlyphs
	if columns*rows > imagefont.MaxGlyphs-used {
		return nil, ErrFull
	}
	added := entry{Hash: hash, Columns: columns, Rows: rows, Start: imagefont.FirstCodepoint + rune(used)}
	cache := filepath.Join(g.directory, "images", hash+".png")
	if err = writeNew(cache, data); errors.Is(err, os.ErrExist) {
		old, readErr := readPrivate(cache, 16<<20)
		if readErr != nil {
			return nil, readErr
		}
		if !bytes.Equal(old, data) {
			return nil, errors.New("image gallery cache content does not match its hash")
		}
	} else if err != nil {
		return nil, err
	}
	next := g.state
	next.Revision++
	next.Images = append(append([]entry(nil), g.state.Images...), added)
	frames := make([]imagefont.Frame, 0, len(next.Images))
	for _, saved := range next.Images {
		var decoded image.Image
		if saved.Hash == hash {
			decoded = normalized
		} else {
			decoded, err = g.cachedImage(saved)
			if err != nil {
				return nil, err
			}
		}
		frames = append(frames, imagefont.Frame{Image: decoded, Columns: saved.Columns, Rows: saved.Rows, CodepointStart: saved.Start})
	}
	return g.build(ctx, next, frames, &added)
}

func (g *Gallery) build(ctx context.Context, next diskState, frames []imagefont.Frame, selected *entry) (*Revision, error) {
	token, err := randomID()
	if err != nil {
		return nil, err
	}
	next.Font = "revision-" + token + ".ttf"
	next.Family = "OpenAI Image Gallery " + next.ID[:8] + " " + token[:8]
	next.PostScript = "OpenAIImages-" + next.ID[:8] + "-" + token + "-Regular"
	encoded, err := imagefont.Encode(ctx, frames, imagefont.Options{Family: next.Family, PostScript: next.PostScript})
	if err != nil {
		return nil, err
	}
	path := filepath.Join(g.directory, "fonts", next.Font)
	if err = writeNew(path, encoded.Data); err != nil {
		return nil, err
	}
	revision := &Revision{FontPath: path, PostScript: next.PostScript, Family: next.Family, ProfileName: "OpenAI Images " + next.ID[:8], state: next, owner: g}
	if selected != nil {
		revision.Text = textFor(*selected)
		revision.Columns = selected.Columns
		revision.Rows = selected.Rows
	}
	g.pending = revision
	return revision, nil
}

func (g *Gallery) existing(selected *entry) *Revision {
	state := g.State()
	revision := &Revision{FontPath: state.FontPath, PostScript: state.PostScript, Family: state.Family, ProfileName: state.ProfileName, Existing: true, state: g.state, owner: g}
	if selected != nil {
		revision.Text = textFor(*selected)
		revision.Columns = selected.Columns
		revision.Rows = selected.Rows
	}
	g.pending = revision
	return revision
}

// Commit atomically publishes a prepared revision after successful activation.
func (g *Gallery) Commit(ctx context.Context, revision *Revision) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	if revision == nil || revision.owner != g || g.pending != revision {
		return errors.New("image gallery revision is stale or belongs to another gallery")
	}
	if revision.Existing {
		g.pending = nil
		return nil
	}
	data, err := json.MarshalIndent(revision.state, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(g.directory, ".state-*")
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
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(g.directory, "state.json")); err != nil {
		return err
	}
	g.state = revision.state
	g.pending = nil
	return nil
}

func (g *Gallery) check(ctx context.Context) error {
	if g.closed {
		return errors.New("image gallery is closed")
	}
	return ctx.Err()
}
func (g *Gallery) validate() error {
	s := g.state
	if s.Version != 1 || !isHex(s.ID, 32) || s.Revision < 0 || !isFontName(s.Font) || s.PostScript == "" || s.Family == "" || len(s.Images) > imagefont.MaxGlyphs {
		return errors.New("invalid image gallery metadata")
	}
	// Stored identities are generated from these exact safe components.
	token := strings.TrimSuffix(strings.TrimPrefix(s.Font, "revision-"), ".ttf")
	if s.PostScript != "OpenAIImages-"+s.ID[:8]+"-"+token+"-Regular" || s.Family != "OpenAI Image Gallery "+s.ID[:8]+" "+token[:8] {
		return errors.New("invalid image gallery font identity")
	}
	if err := checkPrivate(filepath.Join(g.directory, "fonts", s.Font), false); err != nil {
		return err
	}
	next := imagefont.FirstCodepoint
	type placement struct {
		hash    string
		columns int
	}
	seen := map[placement]bool{}
	for _, item := range s.Images {
		count := item.Columns * item.Rows
		key := placement{item.Hash, item.Columns}
		if !isHex(item.Hash, 64) || seen[key] || item.Columns < 1 || item.Columns > 64 || item.Rows < 1 || item.Rows > 32 || item.Start != next || count > int(imagefont.LastCodepoint-next)+1 {
			return errors.New("invalid image gallery character allocation")
		}
		if err := checkPrivate(filepath.Join(g.directory, "images", item.Hash+".png"), false); err != nil {

			return err
		}
		seen[key] = true
		next += rune(count)
	}
	return nil
}
func (g *Gallery) cachedImage(item entry) (image.Image, error) {
	data, err := readPrivate(filepath.Join(g.directory, "images", item.Hash+".png"), 16<<20)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != item.Hash {
		return nil, errors.New("image gallery cache content does not match its hash")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 {
		return nil, errors.New("invalid image gallery cache dimensions")
	}
	return png.Decode(bytes.NewReader(data))
}
func textFor(item entry) string {
	var text strings.Builder
	for i := 0; i < item.Columns*item.Rows; i++ {
		text.WriteRune(item.Start + rune(i))
		if (i+1)%item.Columns == 0 {
			text.WriteByte('\n')
		}
	}
	return text.String()
}
func normalize(ctx context.Context, decoded image.Image) (image.Image, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if decoded == nil || decoded.Bounds().Empty() {
		return nil, nil, errors.New("cannot display an empty image")
	}
	bounds := decoded.Bounds()
	ratio := min(1.0, 1024.0/float64(max(bounds.Dx(), bounds.Dy())))
	width, height := max(1, int(math.Round(float64(bounds.Dx())*ratio))), max(1, int(math.Round(float64(bounds.Dy())*ratio)))
	normalized := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(normalized, normalized.Bounds(), decoded, bounds, draw.Src, nil)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, normalized); err != nil {
		return nil, nil, err
	}
	return normalized, buffer.Bytes(), nil
}
func privateDirectory(path string) error {
	err := os.MkdirAll(path, 0700)
	if err != nil {
		return err
	}
	return checkPrivate(path, true)
}
func checkPrivate(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return errors.New("image gallery storage must use regular files and directories, without symbolic links")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("image gallery storage must be private to its owner (directories 0700, files 0600)")
	}
	return nil
}
func readPrivate(path string, limit int64) ([]byte, error) {
	if err := checkPrivate(path, false); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("image gallery metadata or cache file exceeds its limit")
	}
	return data, nil
}
func writeNew(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	return nil
}
func randomID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
func isHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
func isFontName(name string) bool {
	return strings.HasPrefix(name, "revision-") && strings.HasSuffix(name, ".ttf") && isHex(strings.TrimSuffix(strings.TrimPrefix(name, "revision-"), ".ttf"), 32)
}

func safePathError(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}
