//go:build !darwin

package autocomplete

import "os"

// Linux POSIX ACL writes are reflected in the permission mask checked by
// openPickerDirectory. Other platforms retain their existing directory policy.
func checkPickerDirectoryPermissions(*os.File) error { return nil }
