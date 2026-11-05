package profiles

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This bound applies only to profiles.json, never API bodies or streaming events.
const maxProfileBytes = 1 << 20

var (
	ErrInvalid  = errors.New("invalid profiles; existing profiles were kept")
	ErrChanged  = errors.New("profiles changed during the operation; retry the command")
	ErrTooLarge = errors.New("profiles exceed the 1 MiB local registry limit; existing profiles were kept")
	ErrUnsafe   = errors.New("profiles must be a private regular file owned by the current user")
)

type Values struct {
	Organization *string `json:"organization,omitempty"`
	Project      *string `json:"project,omitempty"`
	// BaseURL preserves endpoints from the earlier draft until explicitly removed.
	BaseURL *string `json:"base_url,omitempty"`
}

type Registry struct {
	Version        int               `json:"version"`
	DefaultProfile *string           `json:"default_profile"`
	Profiles       map[string]Values `json:"profiles"`
}

func Path() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(directory) {
		return "", errors.New("profiles require an absolute user configuration directory")
	}
	return filepath.Join(directory, "openai", "profiles.json"), nil
}

func emptyProfileRegistry() Registry {
	return Registry{Version: 1, Profiles: make(map[string]Values)}
}

func ValidName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && ValidHeader(name)
}

func ValidHeader(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validProfileBaseURL(value string) bool {
	if value == "" {
		return true
	}
	endpoint, err := url.Parse(value)
	return err == nil && ValidHeader(value) && endpoint.Hostname() != "" &&
		(endpoint.Scheme == "http" || endpoint.Scheme == "https") && endpoint.Opaque == "" &&
		endpoint.User == nil && endpoint.RawQuery == "" && !endpoint.ForceQuery && !strings.Contains(value, "#")
}

func validateProfileRegistry(registry Registry) error {
	if registry.Version != 1 || registry.Profiles == nil {
		return ErrInvalid
	}
	if registry.DefaultProfile != nil {
		if _, ok := registry.Profiles[*registry.DefaultProfile]; !ok {
			return ErrInvalid
		}
	}
	for name, values := range registry.Profiles {
		if !ValidName(name) ||
			(values.Organization != nil && !ValidHeader(*values.Organization)) ||
			(values.Project != nil && !ValidHeader(*values.Project)) ||
			(values.BaseURL != nil && !validProfileBaseURL(*values.BaseURL)) {
			return ErrInvalid
		}
	}
	return nil
}

// Token decoding rejects duplicate keys and null field values before Go's JSON
// decoder can replace them with indistinguishable zero values.
func decodeProfiles(data []byte) (Registry, error) {
	var registry Registry
	if !utf8.Valid(data) {
		return registry, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	fields := 0
	err := decodeProfileObject(decoder, func(key string) error {
		fields++
		switch key {
		case "version":
			return decoder.Decode(&registry.Version)
		case "default_profile":
			value, err := decoder.Token()
			if value == nil || err != nil {
				return err
			}
			name, ok := value.(string)
			if !ok {
				return ErrInvalid
			}
			registry.DefaultProfile = &name
			return nil
		case "profiles":
			registry.Profiles = make(map[string]Values)
			return decodeProfileObject(decoder, func(name string) error {
				var values Values
				err := decodeProfileObject(decoder, func(field string) error {
					value, err := decoder.Token()
					text, ok := value.(string)
					if err != nil || !ok {
						return ErrInvalid
					}
					switch field {
					case "organization":
						values.Organization = &text
					case "project":
						values.Project = &text
					case "base_url":
						values.BaseURL = &text
					default:
						return ErrInvalid
					}
					return nil
				})
				registry.Profiles[name] = values
				return err
			})
		default:
			return ErrInvalid
		}
	})
	if err != nil || fields != 3 || decoder.Decode(new(any)) != io.EOF || validateProfileRegistry(registry) != nil {
		return Registry{}, ErrInvalid
	}
	return registry, nil
}

func decodeProfileObject(decoder *json.Decoder, field func(string) error) error {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return ErrInvalid
		}
		seen[name] = true
		if err := field(name); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return ErrInvalid
	}
	return nil
}

func Load(ctx context.Context, path string) (Registry, error) {
	if err := ctx.Err(); err != nil {
		return Registry{}, err
	}
	// A missing registry must not create state or tighten existing config permissions.
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return emptyProfileRegistry(), nil
	} else if err != nil {
		return Registry{}, err
	}
	root, name, err := openDirectory(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return emptyProfileRegistry(), nil
	}
	if err != nil {
		return Registry{}, err
	}
	defer root.Close()
	registry, _, err := readProfiles(ctx, root, name)
	return registry, err
}

func readProfiles(ctx context.Context, root *os.Root, name string) (Registry, os.FileInfo, error) {
	for range 64 {
		if err := ctx.Err(); err != nil {
			return Registry{}, nil, err
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return emptyProfileRegistry(), nil, nil
		}
		if err != nil {
			return Registry{}, nil, err
		}
		registry, err := readProfileSnapshot(ctx, root, name, info)
		if !errors.Is(err, ErrChanged) {
			return registry, info, err
		}
	}
	return Registry{}, nil, ErrChanged
}

func readProfileSnapshot(ctx context.Context, root *os.Root, name string, expected os.FileInfo) (Registry, error) {
	if !privateFile(expected) {
		return Registry{}, ErrUnsafe
	}
	file, err := root.OpenFile(name, os.O_RDONLY|openFlags(), 0)
	if err != nil {
		return Registry{}, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return Registry{}, err
	}
	if !privateFile(actual) || !sameSnapshot(expected, actual) {
		return Registry{}, ErrChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProfileBytes+1))
	if err != nil {
		return Registry{}, err
	}
	if err := ctx.Err(); err != nil {
		return Registry{}, err
	}
	if !profileSnapshotMatches(root, name, actual) {
		return Registry{}, ErrChanged
	}
	if len(data) > maxProfileBytes {
		return Registry{}, ErrTooLarge
	}
	return decodeProfiles(data)
}

func profileSnapshotMatches(root *os.Root, name string, expected os.FileInfo) bool {
	current, err := root.Lstat(name)
	if expected == nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return err == nil && privateFile(current) && sameSnapshot(expected, current)
}

// The callback sees the latest state while the sibling lock is held. It must
// resolve saved-default edits here, not from an earlier unlocked snapshot.
func Update(ctx context.Context, path string, edit func(*Registry) error) (Registry, error) {
	if err := ctx.Err(); err != nil {
		return Registry{}, err
	}
	if edit == nil {
		return Registry{}, ErrInvalid
	}
	root, name, err := openDirectory(path, true)
	if err != nil {
		return Registry{}, err
	}
	defer root.Close()
	directoryInfo, err := root.Stat(".")
	if err != nil {
		return Registry{}, err
	}
	lock, err := lockFile(ctx, root, name)
	if err != nil {
		return Registry{}, err
	}
	defer lock.Close()
	lockInfo, err := lock.Stat()
	if err != nil {
		return Registry{}, err
	}
	registry, snapshot, err := readProfiles(ctx, root, name)
	if err != nil {
		return Registry{}, err
	}
	if err := edit(&registry); err != nil {
		return Registry{}, err
	}
	if err := validateProfileRegistry(registry); err != nil {
		return Registry{}, err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return Registry{}, err
	}
	data = append(data, '\n')
	if len(data) > maxProfileBytes {
		return Registry{}, ErrTooLarge
	}
	temporary := ".profiles-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Registry{}, err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return Registry{}, err
	}
	currentDirectory, err := os.Lstat(filepath.Dir(path))
	if err != nil || !os.SameFile(directoryInfo, currentDirectory) || !privatePermissions(currentDirectory) ||
		!profileSnapshotMatches(root, "."+name+".lock", lockInfo) || !profileSnapshotMatches(root, name, snapshot) {
		return Registry{}, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return Registry{}, err
	}
	// Rename replaces the entry itself, never a substituted symlink's target.
	if err := root.Rename(temporary, name); err != nil {
		return Registry{}, err
	}
	return registry, nil
}
