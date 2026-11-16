package imagegallery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ownerTTY = regexp.MustCompile(`^/dev/ttys[0-9]+$`)

// TerminalSession identifies the current renderer's session-hashed directory.
// Terminal can reuse a TTY after a tab closes, so a TTY alone is not its identity.
type TerminalSession struct {
	Directory string
	TTY       string
}

// BindTTY records the tab that owns these immutable scrollback artifacts. The
// gallery lock makes ownership recording and font activation indivisible with
// respect to cleanup. Older galleries acquire ownership on their next use.
func (g *Gallery) BindTTY(ctx context.Context, tty string) error {
	exists, err := g.checkTTY(ctx, tty)
	if err != nil || exists {
		return err
	}
	return writeNew(ctx, filepath.Join(g.directory, ".tty"), []byte(tty))
}

// CheckTTY reads ownership without binding unowned legacy galleries.
func (g *Gallery) CheckTTY(ctx context.Context, tty string) error {
	_, err := g.checkTTY(ctx, tty)
	return err
}

func (g *Gallery) checkTTY(ctx context.Context, tty string) (bool, error) {
	if err := g.check(ctx); err != nil {
		return false, err
	}
	if !ownerTTY.MatchString(tty) {
		return false, errors.New("invalid image gallery owner TTY")
	}
	data, err := readPrivate(filepath.Join(g.directory, ".tty"), 64)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !ownerTTY.MatchString(string(data)) {
		return true, errors.New("invalid recorded image gallery owner TTY")
	}
	if string(data) != tty {
		return true, ErrOtherTTY
	}
	return true, nil
}

// CleanupClosed reclaims galleries only after inventory returns a complete,
// successful list of Terminal tabs while the gallery lock is held. A live owner
// or a live tab selecting any gallery font preserves its scrollback artifacts.
// Busy, unowned, or malformed galleries are left alone. The stable lock inode
// and its containing directory remain to avoid racing another Open operation.
func CleanupClosed(ctx context.Context, root string, current TerminalSession, inventory func(context.Context) (liveTTYs, liveFonts []string, err error), unregister func(context.Context, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if current.Directory != "" || current.TTY != "" {
		if !ownerTTY.MatchString(current.TTY) || filepath.Dir(current.Directory) != filepath.Clean(root) || !isHex(filepath.Base(current.Directory), 32) {
			return errors.New("invalid current Terminal session")
		}
	}
	if err := checkPrivate(root, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || !isHex(entry.Name(), 32) {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		if directory == current.Directory {
			continue
		}
		if err := checkPrivate(directory, true); err != nil {
			continue
		}
		// Do not create files in old galleries with no recorded owner or in
		// the empty lock tombstones left by an earlier successful cleanup.
		data, err := readPrivate(filepath.Join(directory, ".tty"), 64)
		if err != nil || !ownerTTY.MatchString(string(data)) {
			continue
		}
		lock, err := acquireLock(filepath.Join(directory, ".lock"))
		if err != nil {
			continue
		}
		gallery := &Gallery{directory: directory, lock: lock}
		err = gallery.cleanupClosed(ctx, current, inventory, unregister)
		closeErr := gallery.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
	}
	return nil
}

func (g *Gallery) cleanupClosed(ctx context.Context, current TerminalSession, inventory func(context.Context) ([]string, []string, error), unregister func(context.Context, string) error) error {
	// Recheck after acquiring the lock: another renderer may have bound or
	// initialized this gallery while the inventory was being collected.
	data, err := readPrivate(filepath.Join(g.directory, ".tty"), 64)
	if err != nil || !ownerTTY.MatchString(string(data)) {
		return nil
	}
	tty := string(data)
	data, err = readPrivate(filepath.Join(g.directory, "state.json"), 1<<20)
	if err != nil || g.decodeState(data) != nil || g.validateMetadata() != nil {
		return nil
	}
	liveTTYs, liveFonts, err := inventory(ctx)
	if err != nil {
		return err
	}
	keep := false
	for _, liveTTY := range liveTTYs {
		if !ownerTTY.MatchString(liveTTY) {
			return errors.New("invalid live Terminal tab inventory")
		}
		// The current session hash proves an older directory with this TTY
		// belonged to a closed tab. Still retain it if any tab uses its font.
		if liveTTY == tty && tty != current.TTY {
			keep = true
		}
	}
	for _, font := range liveFonts {
		if font == "" || len(font) > 255 || strings.ContainsAny(font, "\x00\r\n\x1b") {
			return errors.New("invalid live Terminal font inventory")
		}
		if strings.HasPrefix(font, "OpenAIImages-"+g.state.ID[:8]+"-") {
			keep = true
		}
	}
	if keep {
		return nil
	}
	fontDirectory := filepath.Join(g.directory, "fonts")
	for _, name := range []string{"fonts", "images"} {
		if err := checkPrivate(filepath.Join(g.directory, name), true); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	fonts, err := os.ReadDir(fontDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, font := range fonts {
		if !isFontName(font.Name()) {
			return nil
		}
		if err := checkPrivate(filepath.Join(fontDirectory, font.Name()), false); err != nil {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, font := range fonts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unregister(ctx, filepath.Join(fontDirectory, font.Name())); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep every file when unregistration fails so the next invocation can
	// retry using the original URLs. Remove the large artifacts before the
	// ownership marker; the lock itself must never be unlinked.
	for _, name := range []string{"fonts", "images", "state.json", ".pending", ".pending.json", ".tty"} {
		if err := os.RemoveAll(filepath.Join(g.directory, name)); err != nil {
			return err
		}
	}
	return nil
}
