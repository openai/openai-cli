//go:build !darwin

package updatenotice

import "context"

func runHomebrewVerifierHelper([]string) bool { return false }
func mayHaveHomebrewVerifier(string) bool     { return false }
func selectEnrolledHomebrewVerifier(context.Context, *releaseDelivery, *noticeCache) bool {
	return false
}

func validHomebrewReceipt(*noticeCache, string) bool { return true }

func probeHomebrewVerifier(context.Context, *releaseDelivery, string) bool { return false }
