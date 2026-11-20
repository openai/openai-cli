package audiohistory

import (
	"errors"
	"os"
)

// The empty marker is a private preference outside the set of recording names.
const autoplayMarker = ".autoplay"

func autoplayFile(root *os.Root) (os.FileInfo, error) {
	info, err := root.Lstat(autoplayMarker)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err == nil && (!privateMode(info, false) || info.Size() != 0) {
		return nil, ErrInvalid
	}
	return info, err
}

func (s Store) Autoplay() (bool, error) {
	root, err := s.open(false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := autoplayFile(root)
	return info != nil, err
}

func (s Store) SetAutoplay(enabled bool) error {
	root, err := s.open(enabled)
	if !enabled && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := autoplayFile(root)
	if err != nil || enabled == (info != nil) {
		return err
	}
	if !enabled {
		err := removeOwned(root, autoplayMarker, info)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	file, info, err := createPrivateFile(root, autoplayMarker)
	if errors.Is(err, os.ErrExist) {
		_, err = autoplayFile(root)
		return err
	}
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return errors.Join(err, removeOwned(root, autoplayMarker, info))
	}
	return nil
}
