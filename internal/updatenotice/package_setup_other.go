//go:build !linux

package updatenotice

func runLinuxPackageHelper([]string) bool { return false }
