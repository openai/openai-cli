//go:build windows

package autocomplete

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func checkPickerDirectoryPermissions(file *os.File) error {
	return checkPickerWindowsPermissionPolicy(file, false, false, true)
}

func checkPickerChildCreationPermissions(file *os.File) error {
	return checkPickerWindowsPermissionPolicy(file, false, true, true)
}

func checkPickerAncestorDirectory(file *os.File) error {
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &attributes); err != nil {
		return errors.New("cannot inspect shell integration ancestor")
	}
	if attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("shell integration requires ancestors without Windows reparse points")
	}
	return checkPickerWindowsPermissionPolicy(file, false, true, false)
}

func checkPickerAncestorLink(*os.Root, string, os.FileInfo) error {
	// Inspecting a followed handle would validate the target, not who can
	// retarget the link. Keep reparse-point ancestry for explicit manual setup.
	return errors.New("shell integration requires ancestors without Windows reparse points")
}

// Windows mode bits do not describe who can modify a file. Inspect the opened
// object so ownership and access checks apply to the same identity we use.
func checkPickerWindowsPermissions(file *os.File, private bool) error {
	return checkPickerWindowsPermissionPolicy(file, private, false, false)
}

func checkPickerWindowsPermissionPolicy(file *os.File, private, ancestor, children bool) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return errors.New("cannot inspect shell integration owner")
	}
	if children {
		if err := checkPickerWindowsCreationOwner(token, user.User.Sid); err != nil {
			return err
		}
	}
	const information = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, information)
	if err != nil {
		return errors.New("cannot inspect shell integration permissions")
	}
	return validatePickerWindowsPermissionPolicy(descriptor, user.User.Sid, private, ancestor, children)
}

func checkPickerWindowsCreationOwner(token windows.Token, user *windows.SID) error {
	// TOKEN_OWNER contains one SID pointer followed by its data. A native SID
	// has at most 15 subauthorities, so SECURITY_MAX_SID_SIZE is 8 + 15*4.
	var owner struct {
		sid     *windows.SID
		storage [68]byte
	}
	var size uint32
	if err := windows.GetTokenInformation(token, windows.TokenOwner, (*byte)(unsafe.Pointer(&owner)), uint32(unsafe.Sizeof(owner)), &size); err != nil {
		return errors.New("cannot inspect shell integration creation owner")
	}
	// The default owner receives implicit ACL control and replaces inherited
	// CREATOR OWNER entries. Check it before creation, not after exposure.
	if !pickerWindowsManagedOwner(owner.sid, user) {
		return errors.New("shell integration requires newly created files and directories owned by the current user or Administrators")
	}
	return nil
}

func pickerWindowsManagedOwner(owner, user *windows.SID) bool {
	return owner != nil && owner.IsValid() && user != nil && user.IsValid() && (owner.Equals(user) || owner.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

func validatePickerWindowsPermissions(descriptor *windows.SECURITY_DESCRIPTOR, user *windows.SID, private bool) error {
	return validatePickerWindowsPermissionPolicy(descriptor, user, private, false, false)
}

func validatePickerWindowsPermissionPolicy(descriptor *windows.SECURITY_DESCRIPTOR, user *windows.SID, private, ancestor, children bool) error {
	if descriptor == nil || !descriptor.IsValid() || user == nil || !user.IsValid() {
		return errors.New("cannot inspect shell integration permissions")
	}
	owner, _, err := descriptor.Owner()
	// Elevated Windows processes normally create Administrators-owned files.
	// That group already controls the machine and is trusted below as well.
	if err != nil || owner == nil || !(pickerWindowsManagedOwner(owner, user) || ancestor && pickerWindowsSystemPrincipal(owner)) {
		if ancestor {
			return errors.New("shell integration ancestor has an untrusted owner")
		}
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
	const allMutation = windows.ACCESS_MASK(windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE |
		windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild)
	mutation := allMutation
	if ancestor {
		// Normal volume roots let users create subdirectories. That cannot
		// replace an existing protected child. Keep FILE_WRITE_DATA denied:
		// besides creating files, it can set a reparse point on an empty
		// directory before the next protected child has been created.
		mutation &^= windows.FILE_APPEND_DATA
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace == nil {
			return errors.New("cannot inspect shell integration access rules")
		}
		inheritOnly := ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0
		inheritable := children && ace.Header.AceFlags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) != 0
		if ancestor && ace.Header.AceFlags&windows.CONTAINER_INHERIT_ACE == 0 && ace.Header.AceFlags&windows.NO_PROPAGATE_INHERIT_ACE != 0 {
			// This path creates only a directory. File-only inheritance with
			// no propagation does not reach that child or its future files.
			inheritable = false
		}
		if inheritOnly && !inheritable || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		// Do not attempt effective-access evaluation for unfamiliar object or
		// conditional ACEs. Conservatively keep every allow grant instead of
		// subtracting denies based on group membership and ACE ordering.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < uint16(unsafe.Sizeof(*ace)) {
			return errors.New("shell integration has unsupported access rules")
		}
		checkedMutation := mutation
		if inheritable {
			// Check templates before creating even a temporary child. Tightening
			// a child's DACL later cannot revoke a handle opened in the meantime.
			// The ancestor append/create-subdirectory exception does not apply:
			// the same right allows appending to an inherited regular file.
			checkedMutation = allMutation
		}
		if ace.Mask&checkedMutation == 0 && (!private || ace.Mask == 0) {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		// Windows substitutes the creator's owner into this inherited template.
		// Every created child is required to have a trusted owner. This does
		// not trust CREATOR GROUP or an effective CREATOR OWNER entry.
		if inheritable && inheritOnly && sid.IsValid() && sid.IsWellKnown(windows.WinCreatorOwnerSid) {
			continue
		}
		// Local administrators and SYSTEM already control the machine, and
		// their entries are present on normal user profile directories.
		if !sid.IsValid() || !(sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || ancestor && pickerWindowsSystemPrincipal(sid)) {
			if private && ace.Mask&checkedMutation == 0 {
				return errors.New("shell integration lock must be private")
			}
			return errors.New("shell integration must not be writable by other users")
		}
	}
	return nil
}

func pickerWindowsSystemPrincipal(sid *windows.SID) bool {
	// The Windows Modules Installer service SID is stable across installations.
	const trustedInstaller = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	return sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.String() == trustedInstaller
}
