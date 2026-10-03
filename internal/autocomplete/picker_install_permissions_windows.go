//go:build windows

package autocomplete

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func checkPickerDirectoryPermissions(file *os.File) error {
	return checkPickerWindowsPermissions(file, false)
}

// Windows mode bits do not describe who can modify a file. Inspect the opened
// object so ownership and access checks apply to the same identity we use.
func checkPickerWindowsPermissions(file *os.File, private bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("cannot inspect shell integration owner")
	}
	const information = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, information)
	if err != nil {
		return errors.New("cannot inspect shell integration permissions")
	}
	return validatePickerWindowsPermissions(descriptor, user.User.Sid, private)
}

func validatePickerWindowsPermissions(descriptor *windows.SECURITY_DESCRIPTOR, user *windows.SID, private bool) error {
	if descriptor == nil || !descriptor.IsValid() || user == nil || !user.IsValid() {
		return errors.New("cannot inspect shell integration permissions")
	}
	owner, _, err := descriptor.Owner()
	// Elevated Windows processes normally create Administrators-owned files.
	// That group already controls the machine and is trusted below as well.
	if err != nil || owner == nil || !(owner.Equals(user) || owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
		return errors.New("shell integration requires files and directories owned by the current user or Administrators")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		// An absent or NULL DACL allows everyone full access. An empty DACL,
		// in contrast, grants no access and is safe to inspect.
		return errors.New("shell integration requires an explicit access control list")
	}
	// FILE_WRITE_DATA/FILE_APPEND_DATA also mean adding files/subdirectories
	// on directories. FILE_DELETE_CHILD permits replacing a protected child.
	const fileDeleteChild = 0x0040
	const mutation = windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE |
		windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace == nil {
			return errors.New("cannot inspect shell integration access rules")
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		// Do not attempt effective-access evaluation for unfamiliar object or
		// conditional ACEs. Conservatively keep every allow grant instead of
		// subtracting denies based on group membership and ACE ordering.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < uint16(unsafe.Sizeof(*ace)) {
			return errors.New("shell integration has unsupported access rules")
		}
		if ace.Mask&mutation == 0 && (!private || ace.Mask == 0) {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		// Local administrators and SYSTEM already control the machine, and
		// their entries are present on normal user profile directories.
		if !sid.IsValid() || !(sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
			if private && ace.Mask&mutation == 0 {
				return errors.New("shell integration lock must be private")
			}
			return errors.New("shell integration must not be writable by other users")
		}
	}
	return nil
}
