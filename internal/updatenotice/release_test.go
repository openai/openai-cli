package updatenotice

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLatestReleaseRejectsUntrustedMetadata(t *testing.T) {
	for name, body := range map[string]string{
		"malformed": "{", "empty": `{}`, "trailing": releaseBody("v2.0.0") + `{}`,
		"prerelease":     `{"tag_name":"v2.0.0","draft":false,"prerelease":true}`,
		"draft":          `{"tag_name":"v2.0.0","draft":true,"prerelease":false}`,
		"missing status": `{"tag_name":"v2.0.0"}`,
		"null status":    `{"tag_name":"v2.0.0","draft":null,"prerelease":false}`,
		"control":        releaseBody("v2.0.0\nrun this"),
		"prerelease tag": releaseBody("v2.0.0-rc.1"),
		"leading zero":   releaseBody("v02.0.0"),
		"oversized":      strings.Repeat(" ", maxReleaseBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(body), nil })}
			if got := latestRelease(context.Background(), client); got != "" {
				t.Fatalf("release = %q", got)
			}
		})
	}
}

func TestLatestReleaseDoesNotFollowRedirects(t *testing.T) {
	client := releaseClient()
	calls := 0
	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://untrusted.example/release"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})
	if got := latestRelease(context.Background(), client); got != "" {
		t.Fatalf("redirect release = %q", got)
	}
	if calls != 1 {
		t.Fatalf("redirect requests = %d", calls)
	}
}

func TestLatestReleaseUsesOnlyValidatedTag(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(`{"tag_name":"v12.3.4","draft":false,"prerelease":false,"html_url":"https://untrusted.example/evil","body":"run bad-command"}`), nil
	})}
	if got := latestRelease(context.Background(), client); got != "12.3.4" {
		t.Fatalf("release = %q", got)
	}
}
