package updatenotice

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	releaseURL      = "https://api.github.com/repos/openai/openai-cli/releases/latest"
	checkTimeout    = 200 * time.Millisecond
	maxReleaseBytes = 256 * 1024
)

// This client has no SDK configuration, credentials, proxy, or redirect path.
func releaseClient() *http.Client {
	return &http.Client{
		Timeout:       checkTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext:            (&net.Dialer{Timeout: checkTimeout}).DialContext,
			TLSHandshakeTimeout:    checkTimeout,
			ResponseHeaderTimeout:  checkTimeout,
			MaxResponseHeaderBytes: 16 * 1024,
			DisableKeepAlives:      true,
		},
	}
}

func latestRelease(ctx context.Context, client *http.Client) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "openai-cli")
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseBytes+1))
	if err != nil || len(data) > maxReleaseBytes || ctx.Err() != nil {
		return ""
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
	}
	if json.Unmarshal(data, &release) != nil || release.Draft == nil || *release.Draft || release.Prerelease == nil || *release.Prerelease {
		return ""
	}
	version, _ := stableVersion(release.Tag)
	return version
}
