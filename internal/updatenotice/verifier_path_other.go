//go:build !linux

package updatenotice

// Platforms without the Linux package installer retain archive verifier lookup.
func trustedSystemVerifier(path string) bool { return true }
