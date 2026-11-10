// Package darwinacl checks native access controls on opened macOS files.
package darwinacl

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

type Policy int

const (
	Absent   Policy = iota // Replacing the file must not discard any explicit ACL.
	DenyOnly               // A protected shell directory allows no permission grants.
	ReadOnly               // A verifier path can allow reads, but never writes.
)

var errACL = errors.New("protected or unreadable access permissions")

// Check uses the open descriptor so a path replacement cannot substitute another ACL.
func Check(file *os.File, policy Policy) error {
	attributes := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY}
	// attrreference + kauth_filesec + the kernel's maximum 128 kauth_ace entries.
	var data [32 + 44 + 128*24]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), unix.FSOPT_REPORT_FULLSIZE, 0)
	runtime.KeepAlive(file)
	if errno != 0 {
		return errACL
	}
	return check(data[:], policy)
}

func check(data []byte, policy Policy) error {
	invalid := errACL
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
	if policy == Absent || common != unix.ATTR_CMN_RETURNED_ATTRS|unix.ATTR_CMN_EXTENDED_SECURITY || len(data) < 76 {
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
		if flags & ^uint32(0xf|0x1f0) != 0 {
			return invalid
		}
		// A deny entry never grants access.
		if flags&0xf == 2 {
			continue
		}
		// Read data, execute, read attributes, read extended attributes,
		// read security, and synchronize cannot modify files or children.
		const readRights = 1<<1 | 1<<3 | 1<<7 | 1<<9 | 1<<11 | 1<<20
		rights := binary.LittleEndian.Uint32(security[offset+20 : offset+24])
		if policy != ReadOnly || flags&0xf != 1 || rights & ^uint32(readRights) != 0 {
			return invalid
		}
	}
	return nil
}
