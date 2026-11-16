//go:build !darwin && !linux

package updatenotice

func runPackageInstallHelper([]string) bool { return false }
