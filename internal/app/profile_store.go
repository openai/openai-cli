package app

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
	errProfilesInvalid   = errors.New("invalid profiles; existing profiles were kept")
	errProfilesChanged   = errors.New("profiles changed during the operation; retry the command")
	errProfilesTooLarge  = errors.New("profiles exceed the 1 MiB local registry limit; existing profiles were kept")
	errProfileFileUnsafe = errors.New("profiles must be a private regular file owned by the current user")
)

type profileValues struct {
	Organization *string `json:"organization,omitempty"`
	Project      *string `json:"project,omitempty"`
	BaseURL      *string `json:"base_url,omitempty"`
}

type profileRegistry struct {
	Version        int                      `json:"version"`
	DefaultProfile *string                  `json:"default_profile"`
	Profiles       map[string]profileValues `json:"profiles"`
}

func profileStorePath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(directory) {
		return "", errors.New("profiles require an absolute user configuration directory")
	}
	return filepath.Join(directory, "openai", "profiles.json"), nil
}

func emptyProfileRegistry() profileRegistry {
	return profileRegistry{Version: 1, Profiles: make(map[string]profileValues)}
}

func validProfileName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && validProfileHeader(name)
}

func validProfileHeader(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validProfileBaseURL(value string) bool {
	if value == "" {
		return true
	}
	endpoint, err := url.Parse(value)
	return err == nil && validProfileHeader(value) && endpoint.Hostname() != "" &&
		(endpoint.Scheme == "http" || endpoint.Scheme == "https") && endpoint.Opaque == "" &&
		endpoint.User == nil && endpoint.RawQuery == "" && !endpoint.ForceQuery && !strings.Contains(value, "#")
}

func validateProfileRegistry(registry profileRegistry) error {
	if registry.Version != 1 || registry.Profiles == nil {
		return errProfilesInvalid
	}
	if registry.DefaultProfile != nil {
		if _, ok := registry.Profiles[*registry.DefaultProfile]; !ok {
			return errProfilesInvalid
		}
	}
	for name, values := range registry.Profiles {
		if !validProfileName(name) ||
			(values.Organization != nil && !validProfileHeader(*values.Organization)) ||
			(values.Project != nil && !validProfileHeader(*values.Project)) ||
			(values.BaseURL != nil && !validProfileBaseURL(*values.BaseURL)) {
			return errProfilesInvalid
		}
	}
	return nil
}

// Token decoding rejects duplicate keys and null field values before Go's JSON
// decoder can replace them with indistinguishable zero values.
func decodeProfiles(data []byte) (profileRegistry, error) {
	var registry profileRegistry
	if !utf8.Valid(data) {
		return registry, errProfilesInvalid
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
				return errProfilesInvalid
			}
			registry.DefaultProfile = &name
			return nil
		case "profiles":
			registry.Profiles = make(map[string]profileValues)
			return decodeProfileObject(decoder, func(name string) error {
				var values profileValues
				err := decodeProfileObject(decoder, func(field string) error {
					value, err := decoder.Token()
					text, ok := value.(string)
					if err != nil || !ok {
						return errProfilesInvalid
					}
					switch field {
					case "organization":
						values.Organization = &text
					case "project":
						values.Project = &text
					case "base_url":
						values.BaseURL = &text
					default:
						return errProfilesInvalid
					}
					return nil
				})
				registry.Profiles[name] = values
				return err
			})
		default:
			return errProfilesInvalid
		}
	})
	if err != nil || fields != 3 || decoder.Decode(new(any)) != io.EOF || validateProfileRegistry(registry) != nil {
		return profileRegistry{}, errProfilesInvalid
	}
	return registry, nil
}

func decodeProfileObject(decoder *json.Decoder, field func(string) error) error {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errProfilesInvalid
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return errProfilesInvalid
		}
		seen[name] = true
		if err := field(name); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return errProfilesInvalid
	}
	return nil
}

func loadProfiles(ctx context.Context, path string) (profileRegistry, error) {
	if err := ctx.Err(); err != nil {
		return profileRegistry{}, err
	}
	// A missing registry must not create state or tighten existing config permissions.
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return emptyProfileRegistry(), nil
	} else if err != nil {
		return profileRegistry{}, err
	}
	root, name, err := openProjectLinkDirectory(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return emptyProfileRegistry(), nil
	}
	if err != nil {
		return profileRegistry{}, profileStoreError(err)
	}
	defer root.Close()
	registry, _, err := readProfiles(ctx, root, name)
	return registry, err
}

func readProfiles(ctx context.Context, root *os.Root, name string) (profileRegistry, os.FileInfo, error) {
	for range 64 {
		if err := ctx.Err(); err != nil {
			return profileRegistry{}, nil, err
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return emptyProfileRegistry(), nil, nil
		}
		if err != nil {
			return profileRegistry{}, nil, err
		}
		registry, err := readProfileSnapshot(ctx, root, name, info)
		if !errors.Is(err, errProfilesChanged) {
			return registry, info, err
		}
	}
	return profileRegistry{}, nil, errProfilesChanged
}

func readProfileSnapshot(ctx context.Context, root *os.Root, name string, expected os.FileInfo) (profileRegistry, error) {
	if !privateProjectLinkFile(expected) {
		return profileRegistry{}, errProfileFileUnsafe
	}
	file, err := root.OpenFile(name, os.O_RDONLY|projectLinkOpenFlags(), 0)
	if err != nil {
		return profileRegistry{}, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return profileRegistry{}, err
	}
	if !privateProjectLinkFile(actual) || !sameProjectLinkSnapshot(expected, actual) {
		return profileRegistry{}, errProfilesChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProfileBytes+1))
	if err != nil {
		return profileRegistry{}, err
	}
	if err := ctx.Err(); err != nil {
		return profileRegistry{}, err
	}
	if !profileSnapshotMatches(root, name, actual) {
		return profileRegistry{}, errProfilesChanged
	}
	if len(data) > maxProfileBytes {
		return profileRegistry{}, errProfilesTooLarge
	}
	return decodeProfiles(data)
}

func profileSnapshotMatches(root *os.Root, name string, expected os.FileInfo) bool {
	current, err := root.Lstat(name)
	if expected == nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return err == nil && privateProjectLinkFile(current) && sameProjectLinkSnapshot(expected, current)
}

// The callback sees the latest state while the sibling lock is held. It must
// resolve saved-default edits here, not from an earlier unlocked snapshot.
func updateProfiles(ctx context.Context, path string, edit func(*profileRegistry) error) (profileRegistry, error) {
	if err := ctx.Err(); err != nil {
		return profileRegistry{}, err
	}
	if edit == nil {
		return profileRegistry{}, errProfilesInvalid
	}
	root, name, err := openProjectLinkDirectory(path, true)
	if err != nil {
		return profileRegistry{}, profileStoreError(err)
	}
	defer root.Close()
	directoryInfo, err := root.Stat(".")
	if err != nil {
		return profileRegistry{}, err
	}
	lock, err := lockProjectLinks(ctx, root, name)
	if err != nil {
		return profileRegistry{}, profileStoreError(err)
	}
	defer lock.Close()
	lockInfo, err := lock.Stat()
	if err != nil {
		return profileRegistry{}, err
	}
	registry, snapshot, err := readProfiles(ctx, root, name)
	if err != nil {
		return profileRegistry{}, err
	}
	if err := edit(&registry); err != nil {
		return profileRegistry{}, err
	}
	if err := validateProfileRegistry(registry); err != nil {
		return profileRegistry{}, err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return profileRegistry{}, err
	}
	data = append(data, '\n')
	if len(data) > maxProfileBytes {
		return profileRegistry{}, errProfilesTooLarge
	}
	temporary := ".profiles-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return profileRegistry{}, err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return profileRegistry{}, err
	}
	currentDirectory, err := os.Lstat(filepath.Dir(path))
	if err != nil || !os.SameFile(directoryInfo, currentDirectory) || !privateProjectLinkPermissions(currentDirectory) ||
		!profileSnapshotMatches(root, "."+name+".lock", lockInfo) || !profileSnapshotMatches(root, name, snapshot) {
		return profileRegistry{}, errProfilesChanged
	}
	if err := ctx.Err(); err != nil {
		return profileRegistry{}, err
	}
	// Rename replaces the entry itself, never a substituted symlink's target.
	if err := root.Rename(temporary, name); err != nil {
		return profileRegistry{}, err
	}
	return registry, nil
}

func profileStoreError(err error) error {
	switch {
	case errors.Is(err, errProjectLinkDirectoryUnsafe):
		return errors.New("profiles require a private configuration directory owned by the current user")
	case errors.Is(err, errProjectLinkLockUnsafe):
		return errors.New("profile lock must be a private regular file owned by the current user")
	case errors.Is(err, errProjectLinkLockChanged), errors.Is(err, errProjectLinkDirectoryChanged):
		return errProfilesChanged
	default:
		return err
	}
}
