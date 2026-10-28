package autocomplete

import "errors"

type pickerSecurityLabel struct {
	present bool
	value   string
}

// SELinux labels are expected on newly created Linux files. All other xattrs
// remain protected. Read through the opened descriptor supplied by the caller.
func readPickerSecurityLabel(list, read func([]byte) (int, error)) (pickerSecurityLabel, error) {
	invalid := errors.New("shell startup file has protected or unreadable extended metadata")
	var names [len("security.selinux") + 1]byte
	count, err := list(names[:])
	if err != nil || count < 0 || count > len(names) {
		return pickerSecurityLabel{}, invalid
	}
	if count == 0 {
		return pickerSecurityLabel{}, nil
	}
	if string(names[:count]) != "security.selinux\x00" {
		return pickerSecurityLabel{}, invalid
	}
	size, err := read(nil)
	// Linux's VFS caps xattr values at 64 KiB; this is not an API body limit.
	if err != nil || size < 0 || size > 65536 {
		return pickerSecurityLabel{}, invalid
	}
	value := make([]byte, size)
	count, err = read(value)
	if err != nil || count != size {
		return pickerSecurityLabel{}, invalid
	}
	return pickerSecurityLabel{present: true, value: string(value)}, nil
}

func comparePickerSecurityLabels(existing, staged func() (pickerSecurityLabel, error)) error {
	before, err := existing()
	if err != nil {
		return err
	}
	after, err := staged()
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("shell startup replacement has different security labels; existing file was kept")
	}
	return nil
}
