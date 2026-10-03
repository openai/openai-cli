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

// Darwin metadata checks already refuse all attributes on both files.
func checkPickerReplacementLabels(*os.File, *os.File) error { return nil }

// Replacing a startup file with a new inode must not discard its extended
// attributes, ACL, or BSD flags. Leave these profiles for manual configuration.
func checkPickerFileMetadata(file *os.File) error {
	if err := checkPickerFileMode(file); err != nil {
		return err
	}
	if err := checkPickerFileLinks(file); err != nil {
		return err
	}
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

// Darwin ACLs can grant writes without changing POSIX mode bits. A profile's
// immediate parent may have the standard home-directory deny-delete ACL, so
// accept deny-only ACLs while conservatively refusing any grant or unknown form.
func checkPickerDirectoryPermissions(file *os.File) error {
	attributes := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY}
	// attrreference + kauth_filesec + the kernel's maximum 128 kauth_ace entries.
	var data [32 + 44 + 128*24]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), unix.FSOPT_REPORT_FULLSIZE, 0)
	runtime.KeepAlive(file)
	if errno != 0 {
		return errors.New("shell directory has protected or unreadable access permissions")
	}
	return checkPickerDarwinDirectoryACL(data[:])
}

// The ancestry check already rejects every Darwin ACL permission grant.
func checkPickerChildCreationPermissions(*os.File) error { return nil }

func checkPickerDarwinDirectoryACL(data []byte) error {
	invalid := errors.New("shell directory has protected or unreadable access permissions")
	if len(data) < 32 {
		return invalid
	}
	size := binary.LittleEndian.Uint32(data[:4])
	if size < 32 || uint64(size) > uint64(len(data)) {
		return invalid
	}
	data = data[:size]
	for _, value := range data[8:24] {
		if value != 0 {
			return invalid
		}
	}
	common := binary.LittleEndian.Uint32(data[4:8])
	if common == unix.ATTR_CMN_RETURNED_ATTRS {
		if len(data) != 32 {
			return invalid
		}
		for _, value := range data[24:] {
			if value != 0 {
				return invalid
			}
		}
		return nil
	}
	if common != unix.ATTR_CMN_RETURNED_ATTRS|unix.ATTR_CMN_EXTENDED_SECURITY || len(data) < 76 {
		return invalid
	}
	// The reference is relative to byte 24. Require the exact bounded layout
	// requested above, with no unrecognized payload or trailing attributes.
	if binary.LittleEndian.Uint32(data[24:28]) != 8 || binary.LittleEndian.Uint32(data[28:32]) != uint32(len(data)-32) {
		return invalid
	}
	security := data[32:]
	if binary.LittleEndian.Uint32(security[:4]) != 0x012cc16d {
		return invalid
	}
	count := binary.LittleEndian.Uint32(security[36:40])
	// Unknown ACL flags are left untouched. The kernel-private low bits and
	// documented no-inherit flag do not grant permissions; defer-inherit does.
	if binary.LittleEndian.Uint32(security[40:44]) & ^uint32(0xffff|1<<17) != 0 || count > 128 || len(security) != 44+int(count)*24 {
		return invalid
	}
	for offset := 44; offset < len(security); offset += 24 {
		flags := binary.LittleEndian.Uint32(security[offset+16 : offset+20])
		if flags&0xf != 2 || flags & ^uint32(0xf|0x1f0) != 0 {
			return invalid
		}
	}
	return nil
}
