package updatenotice

import (
	"os"

	"github.com/openai/openai-cli/internal/darwinacl"
	"golang.org/x/sys/unix"
)

const profileDirectoryAccess = unix.O_SEARCH

func profileACLAllowed(file *os.File) bool {
	return darwinacl.Check(file, darwinacl.ReadOnly) == nil
}
