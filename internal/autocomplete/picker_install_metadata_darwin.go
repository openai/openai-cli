//go:build darwin

package autocomplete

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Replacing a startup file with a new inode must not discard its extended
// attributes, ACL, or BSD flags. Leave these profiles for manual configuration.
func checkPickerFileMetadata(file *os.File) error {
	count, err := unix.Flistxattr(int(file.Fd()), nil)
	if err != nil || count != 0 {
		return errors.New("shell startup file has protected or unreadable extended metadata")
	}
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil || info.Flags != 0 {
		return errors.New("shell startup file has protected or unreadable file flags")
	}
	// Darwin excludes ACLs from listxattr. fgetattrlist reads the opened file
	// directly; x/sys exposes its syscall number and native attribute layout.
	// With no ACL, the response contains only the length, returned-attribute
	// bitmaps, and an empty attribute reference (32 bytes). Any ACL, including
	// an empty but explicit one, or an unknown response is kept unchanged.
	attributes := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY}
	var data [32]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), unix.FSOPT_REPORT_FULLSIZE, 0)
	runtime.KeepAlive(file)
	if errno != 0 || binary.LittleEndian.Uint32(data[:4]) != uint32(len(data)) || binary.LittleEndian.Uint32(data[4:8]) != unix.ATTR_CMN_RETURNED_ATTRS {
		return errors.New("shell startup file has protected or unreadable access permissions")
	}
	for _, value := range data[8:] {
		if value != 0 {
			return errors.New("shell startup file has protected or unreadable access permissions")
		}
	}
	return nil
}
