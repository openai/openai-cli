package updatenotice

import (
	"os"

	"golang.org/x/sys/unix"
)

const profileDirectoryAccess = unix.O_PATH

// Linux mode bits include the ACL mask for named users and groups.
func profileACLAllowed(*os.File) bool { return true }
