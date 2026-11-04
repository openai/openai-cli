//go:build !darwin && !linux && !windows

package updatenotice

import (
	"os"
	"os/exec"
)

func enableZshPath(string, string) bool { return false }

func automaticPlatform() bool          { return false }
func ownedUpdatePath(os.FileInfo) bool { return false }
func startUpdateWorker(string, string) {}
func isolateVerifier(*exec.Cmd)        {}
