//go:build !darwin && !linux

package updatenotice

import (
	"os"
	"os/exec"
)

func automaticPlatform() bool          { return false }
func ownedUpdatePath(os.FileInfo) bool { return false }
func startUpdateWorker(string, string) {}
func isolateVerifier(*exec.Cmd)        {}
