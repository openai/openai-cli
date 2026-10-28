package updatenotice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"syscall"
	"time"
)

const stateName = "state.json"

var errCache = errors.New("update notice cache is unavailable")

type cacheState struct {
	Schema   int    `json:"schema"`
	Checked  int64  `json:"checked"`
	Notified int64  `json:"notified"`
	Latest   string `json:"latest"`
}

type noticeCache struct {
	ctx               context.Context
	directory         string
	directoryIdentity os.FileInfo
	root              *os.Root
	lock              *os.File
	identity          os.FileInfo
	stateIdentity     os.FileInfo
	stateData         []byte
}

func openCache(ctx context.Context, directory string) (*noticeCache, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || !privateMode(info) {
		return nil, errCache
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	cache := &noticeCache{ctx: ctx, directory: directory, directoryIdentity: info, root: root}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		cache.close()
		return nil, errCache
	}
	// Permanent kernel locks release on process exit. Contenders never wait.
	lock, err := root.OpenFile(".lock", os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NONBLOCK, 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := root.Lstat(".lock")
		if statErr != nil || !privateFile(info) {
			cache.close()
			return nil, errCache
		}
		lock, err = root.OpenFile(".lock", os.O_RDWR|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		cache.close()
		return nil, err
	}
	cache.lock = lock
	cache.identity, err = lock.Stat()
	if err != nil || !cache.validLock() {
		cache.close()
		return nil, errCache
	}
	locked, err := tryLock(lock)
	if err != nil || !locked || !cache.validLock() {
		cache.close()
		return nil, errCache
	}
	return cache, nil
}

func (cache *noticeCache) close() {
	if cache.lock != nil {
		cache.lock.Close()
	}
	cache.root.Close()
}

func (cache *noticeCache) validLock() bool {
	if cache.ctx.Err() != nil || !privateFile(cache.identity) {
		return false
	}
	directory, err := os.Lstat(cache.directory)
	if err != nil || !directory.IsDir() || !privateMode(directory) || !os.SameFile(cache.directoryIdentity, directory) {
		return false
	}
	info, err := cache.root.Lstat(".lock")
	return err == nil && privateFile(info) && os.SameFile(cache.identity, info)
}

func privateMode(info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0
}

func privateFile(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && privateMode(info)
}

func (cache *noticeCache) load(now time.Time) (cacheState, error) {
	state := cacheState{Schema: 1}
	data, err := cache.readState()
	if err != nil || data == nil {
		return state, err
	}
	if !decodeState(data, &state) {
		return state, errCache
	}
	if state.Checked <= 0 || state.Checked > now.Unix() || state.Notified < 0 || state.Notified > now.Unix() {
		return state, errCache
	}
	if state.Latest != "" {
		version, ok := stableVersion(state.Latest)
		if !ok || version != state.Latest {
			return state, errCache
		}
	}
	return state, nil
}

func (cache *noticeCache) readState() ([]byte, error) {
	info, err := cache.root.Lstat(stateName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !privateFile(info) {
		return nil, errCache
	}
	file, err := cache.root.OpenFile(stateName, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !privateFile(actual) || !os.SameFile(info, actual) {
		return nil, errCache
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return nil, errCache
	}
	cache.stateIdentity, cache.stateData = actual, data
	return data, nil
}

// Cooperating writers hold the permanent lock. Also preserve distinguishable
// replacements or edits from programs that do not honor that lock.
func (cache *noticeCache) unchangedState() bool {
	info, err := cache.root.Lstat(stateName)
	if cache.stateIdentity == nil {
		return errors.Is(err, os.ErrNotExist)
	}
	if err != nil || !privateFile(info) || !os.SameFile(info, cache.stateIdentity) {
		return false
	}
	file, err := cache.root.OpenFile(stateName, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !privateFile(actual) || !os.SameFile(actual, cache.stateIdentity) {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	return err == nil && cache.ctx.Err() == nil && bytes.Equal(data, cache.stateData)
}

func decodeState(data []byte, state *cacheState) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return false
		}
		switch key {
		case "schema":
			err = json.Unmarshal(value, &state.Schema)
		case "checked":
			err = json.Unmarshal(value, &state.Checked)
		case "notified":
			err = json.Unmarshal(value, &state.Notified)
		case "latest":
			err = json.Unmarshal(value, &state.Latest)
		default:
			return false
		}
		if err != nil {
			return false
		}
	}
	token, err = decoder.Token()
	return err == nil && token == json.Delim('}') && len(seen) == 4 && state.Schema == 1 && decoder.Decode(new(any)) == io.EOF
}

func (cache *noticeCache) save(state cacheState) error {
	return cache.saveState(state)
}

func (cache *noticeCache) saveState(state any) error {
	if !cache.validLock() || !cache.unchangedState() {
		return errCache
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	name := ".state-" + rand.Text()
	file, err := cache.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer cache.root.Remove(name)
	data = append(data, '\n')
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	identity, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, statErr, closeErr); err != nil {
		return err
	}
	if !cache.validLock() || !cache.unchangedState() {
		return errCache
	}
	if err := cache.root.Rename(name, stateName); err != nil {
		return err
	}
	cache.stateIdentity, cache.stateData = identity, data
	return nil
}
