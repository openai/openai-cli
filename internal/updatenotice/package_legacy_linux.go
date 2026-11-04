package updatenotice

import "context"

// Linux package enrollment has recorded the initial executable since introduction.
func legacyPackageIdentity(context.Context, *noticeCache, *updateInstallation, string) (packageIdentity, error) {
	return packageIdentity{}, errCache
}
