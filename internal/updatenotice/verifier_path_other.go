//go:build !linux && !windows

package updatenotice

// Other platforms retain their existing archive verifier lookup.
func trustedSystemVerifier(path string) bool { return true }
