package custom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var (
	errProjectLinkAliasConflict = errors.New("equivalent folder paths select different projects")
	errProjectLinkAliasAccess   = errors.New("could not verify equivalent folder paths")
)

// Normalization only selects candidates. The filesystem decides whether their
// directory entries are equivalent; distinct case-sensitive paths stay distinct.
func projectLinkAliasKeys(ctx context.Context, links map[string]string, directory string) ([]string, error) {
	var keys []string
	normalized := norm.NFC.String(directory)
	for key := range links {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if key == directory {
			keys = append(keys, key)
			continue
		}
		if !strings.EqualFold(norm.NFC.String(key), normalized) {
			continue
		}
		same, err := equivalentProjectLinkDirectories(ctx, key, directory)
		if err != nil {
			return nil, errors.Join(errProjectLinkAliasAccess, err)
		}
		if same {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys, ctx.Err()
}

func equivalentProjectLinkDirectories(ctx context.Context, stored, current string) (bool, error) {
	var paths [][2]string
	for {
		paths = append(paths, [2]string{stored, current})
		parentStored, parentCurrent := filepath.Dir(stored), filepath.Dir(current)
		if parentStored == stored || parentCurrent == current {
			if parentStored != stored || parentCurrent != current {
				return false, nil
			}
			break
		}
		stored, current = parentStored, parentCurrent
	}
	// Check from the root so a distinct sibling is rejected before descending
	// into it. Lstat plus IsDir also excludes replaced symlinks and junctions:
	// a moved folder must not inherit a saved path through a new redirect.
	for i := len(paths) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		left, err := os.Lstat(paths[i][0])
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !left.IsDir() {
			return false, nil
		}
		right, err := os.Lstat(paths[i][1])
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !right.IsDir() || !os.SameFile(left, right) {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func nearestProjectLink(ctx context.Context, links map[string]string, directory string) (string, string, error) {
	for {
		keys, err := projectLinkAliasKeys(ctx, links, directory)
		if err != nil {
			return "", "", err
		}
		if len(keys) != 0 {
			project := links[keys[0]]
			for _, key := range keys[1:] {
				if links[key] != project {
					return "", "", errProjectLinkAliasConflict
				}
			}
			// Report the matched ancestor in the caller's spelling. An alias of
			// the current folder is its own link, not an inherited parent link.
			return directory, project, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", "", nil
		}
		directory = parent
	}
}
