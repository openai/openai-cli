//go:build !darwin && !windows

package autocomplete

import "os"

// Linux POSIX ACL writes are reflected in the permission mask checked by
// openPickerDirectory. Other platforms retain their existing directory policy.
func checkPickerDirectoryPermissions(*os.File) error { return nil }

// Restrictive creation modes bound inherited POSIX ACL permissions.
func checkPickerChildCreationPermissions(*os.File) error { return nil }
