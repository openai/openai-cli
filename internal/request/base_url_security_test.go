package request

import (
	"strings"
	"testing"
)

func TestBaseURLTransportSecurity(t *testing.T) {
	for _, value := range []string{
		"", "https://api.openai.com/v1", "https://gateway.example.test:8443/v1",
		"https://caf\u00e9.example/v1", "http://localhost:8080/v1", "http://LOCALHOST/v1",
		"http://127.0.0.1", "http://127.255.255.254:8080", "http://[::1]:8080/v1",
		"http://[::ffff:127.0.0.1]:8080/v1",
	} {
		t.Run("allowed/"+value, func(t *testing.T) {
			if err := ValidateBaseURL(value, "--base-url"); err != nil {
				t.Errorf("legitimate endpoint rejected: %v", err)
			}
		})
	}
	for _, value := range []string{
		"http://remote.example/v1", "http://192.0.2.1/v1", "http://10.0.0.1/v1",
		"http://[2001:db8::1]/v1", "http://[::ffff:192.0.2.1]/v1",
		"http://localhost.example/v1", "http://127.0.0.1.example/v1",
		"http://localhost@remote.example/v1", "http://127.0.0.1@remote.example/v1",
		"http://127.1/v1", "http://2130706433/v1", "http://0177.0.0.1/v1", "http://0x7f000001/v1",
		"http://localhost./v1", "http://[::1%25synthetic]/v1", "http://[localhost]/v1",
		"http:///v1", "https:///v1", "http://", "http:localhost", "//localhost/v1",
		"http://localhost:bad/v1", "http://[::1/v1", "http://localhost%2eremote.example/v1",
		"http://fake-user:fake-password@remote.example/v1?token=fake-private",
	} {
		for _, source := range []string{"--base-url", "OPENAI_BASE_URL"} {
			t.Run("rejected/"+source+"/"+value, func(t *testing.T) {
				err := ValidateBaseURL(value, source)
				if err == nil {
					t.Fatal("unsafe or malformed endpoint accepted")
				}
				if !strings.Contains(err.Error(), source) || strings.Contains(err.Error(), "fake-") {
					t.Errorf("expected safe, source-specific diagnostic, got %q", err)
				}
			})
		}
	}
}
