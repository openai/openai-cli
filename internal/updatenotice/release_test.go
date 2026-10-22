package updatenotice

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestReleaseClientHonorsProxyEnvironment(t *testing.T) {
	const childKey = "OPENAI_CLI_RELEASE_PROXY_TEST"
	for _, tc := range []struct {
		name, proxyKey, proxy, bypassKey, bypass, want string
	}{
		{"uppercase", "HTTPS_PROXY", "http://proxy.example.test:8080", "NO_PROXY", "", "proxy.example.test:8080"},
		{"lowercase", "https_proxy", "http://proxy.example.test:8080", "no_proxy", "", "proxy.example.test:8080"},
		{"https proxy", "HTTPS_PROXY", "https://proxy.example.test:8443", "NO_PROXY", "", "proxy.example.test:8443"},
		{"uppercase bypass", "HTTPS_PROXY", "http://proxy.example.test:8080", "NO_PROXY", "api.github.com", "api.github.com:443"},
		{"lowercase bypass", "https_proxy", "http://proxy.example.test:8080", "no_proxy", ".github.com", "api.github.com:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if os.Getenv(childKey) != tc.name {
				// net/http caches proxy variables, so each environment needs a fresh process.
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReleaseClientHonorsProxyEnvironment$/^"+strings.ReplaceAll(tc.name, " ", "_")+"$")
				child.WaitDelay = time.Second
				child.Env = []string{childKey + "=" + tc.name, tc.proxyKey + "=" + tc.proxy, tc.bypassKey + "=" + tc.bypass,
					"HTTP_PROXY=http://wrong-proxy.example.test:9999", "http_proxy=http://wrong-proxy.example.test:9999"}
				if output, err := child.CombinedOutput(); err != nil {
					t.Fatalf("proxy check failed: %v\n%s", err, output)
				}
				return
			}

			client := releaseClient()
			transport := client.Transport.(*http.Transport)
			dialed := make(chan string, 1)
			transport.DialContext = func(_ context.Context, _, address string) (net.Conn, error) {
				dialed <- address
				return nil, errors.New("synthetic dial failure")
			}
			if got := latestRelease(t.Context(), client); got != "" {
				t.Fatalf("failed request returned release %q", got)
			}
			select {
			case address := <-dialed:
				if address != tc.want {
					t.Fatalf("dialed %q; want %q", address, tc.want)
				}
			default:
				t.Fatal("release request did not dial")
			}
		})
	}
}

func TestReleaseClientKeepsProxyCredentialsOnCONNECT(t *testing.T) {
	const childKey = "OPENAI_CLI_RELEASE_CONNECT_TEST"
	if os.Getenv(childKey) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReleaseClientKeepsProxyCredentialsOnCONNECT$")
		child.WaitDelay = time.Second
		child.Env = []string{
			childKey + "=1", "HTTPS_PROXY=http://fake-proxy-user:fake-proxy-password@proxy.example.test:8080",
			"OPENAI_API_KEY=fake-api-key", "OPENAI_ADMIN_KEY=fake-admin-key", "OPENAI_WEBHOOK_SECRET=fake-webhook-secret",
			"OPENAI_ORG_ID=org-fake", "OPENAI_PROJECT_ID=proj-fake", "OPENAI_BASE_URL=https://other.example.test",
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic/cert.pem", "OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic/key.pem",
		}
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("CONNECT check failed: %v\n%s", err, output)
		}
		return
	}

	certificate, trust := releaseTestServerCertificate(t)
	originalTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{certificate}}}
	defer func() { http.DefaultTransport = originalTransport }()
	client := releaseClient()
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig != nil || client.Jar != nil {
		t.Fatal("release client inherited TLS client configuration or a cookie jar")
	}
	redirect, err := http.NewRequest(http.MethodGet, "https://other.example.test/release", nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.CheckRedirect == nil || client.CheckRedirect(redirect, nil) != http.ErrUseLastResponse {
		t.Fatal("release client does not reject redirects")
	}
	// Add only the synthetic GitHub-origin certificate to the production transport's trust roots.
	transport.TLSClientConfig = &tls.Config{RootCAs: trust}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	clientSide, proxySide := net.Pipe()
	defer clientSide.Close()
	defer proxySide.Close()
	deadline, _ := ctx.Deadline()
	if err := proxySide.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	stop := context.AfterFunc(ctx, func() {
		clientSide.Close()
		proxySide.Close()
	})
	defer stop()
	var dials atomic.Int32
	transport.DialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		if address != "proxy.example.test:8080" || dials.Add(1) != 1 {
			return nil, errors.New("unexpected release connection")
		}
		return clientSide, nil
	}
	requests := make(chan *http.Request, 2)
	finished := make(chan error, 1)
	go func() {
		defer proxySide.Close()
		defer close(requests)
		finished <- releaseTestProxyExchange(proxySide, certificate, requests)
	}()
	got := latestRelease(ctx, client)
	// Close both ends before joining the fixture, including when request construction failed.
	clientSide.Close()
	proxySide.Close()
	if err := <-finished; err != nil {
		t.Fatalf("proxy fixture failed: %v", err)
	}
	if got != "2.0.0" {
		t.Fatalf("proxied release = %q", got)
	}
	connect, origin := <-requests, <-requests
	if connect == nil || origin == nil {
		t.Fatal("proxy fixture did not capture both requests")
	}
	if connect.Method != http.MethodConnect || connect.Host != "api.github.com:443" {
		t.Fatalf("unexpected CONNECT target: %s %s", connect.Method, connect.Host)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("fake-proxy-user:fake-proxy-password"))
	if connect.Header.Get("Proxy-Authorization") != wantAuth {
		t.Fatal("CONNECT did not receive the synthetic proxy credentials")
	}
	if origin.Method != http.MethodGet || "https://"+origin.Host+origin.URL.RequestURI() != releaseURL {
		t.Fatalf("unexpected origin request: %s %s %s", origin.Method, origin.Host, origin.URL)
	}
	for _, header := range []string{"Authorization", "Api-Key", "X-Api-Key", "Cookie", "OpenAI-Organization", "OpenAI-Project"} {
		if connect.Header.Get(header) != "" || origin.Header.Get(header) != "" {
			t.Errorf("release exchange included %s", header)
		}
	}
	if origin.Header.Get("Proxy-Authorization") != "" {
		t.Error("proxy credentials reached the GitHub-origin request")
	}
}

func releaseTestProxyExchange(connection net.Conn, certificate tls.Certificate, requests chan<- *http.Request) error {
	connect, err := http.ReadRequest(bufio.NewReader(connection))
	if err != nil {
		return fmt.Errorf("read CONNECT: %w", err)
	}
	defer connect.Body.Close()
	requests <- connect
	if _, err := io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return fmt.Errorf("write CONNECT response: %w", err)
	}
	secure := tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequestClientCert})
	origin, err := http.ReadRequest(bufio.NewReader(secure))
	if err != nil {
		return fmt.Errorf("read origin request: %w", err)
	}
	defer origin.Body.Close()
	requests <- origin
	if len(secure.ConnectionState().PeerCertificates) != 0 {
		return errors.New("release client sent a TLS client certificate")
	}
	body := releaseBody("v2.0.0")
	reply := &http.Response{
		StatusCode: http.StatusOK, ProtoMajor: 1, ProtoMinor: 1, Close: true,
		ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body)),
	}
	if err := reply.Write(secure); err != nil {
		return fmt.Errorf("write origin response: %w", err)
	}
	return nil
}

func releaseTestServerCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"api.github.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	trust := x509.NewCertPool()
	trust.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, trust
}

func TestReleaseDialerFallsBackBeforeDeadline(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dialer := releaseDialer()
	dialer.Resolver = &net.Resolver{PreferGo: true, Dial: releaseTestDNSConnection}
	started, stopped := make(chan struct{}), make(chan struct{})
	dialer.ControlContext = func(ctx context.Context, network, _ string, _ syscall.RawConn) error {
		if network == "tcp6" {
			close(started)
			defer close(stopped)
			<-ctx.Done() // Hold the primary attempt while the actual dialer starts its IPv4 fallback.
			return ctx.Err()
		}
		return nil
	}
	connection, dialErr := dialer.DialContext(t.Context(), "tcp", net.JoinHostPort("release-dial.test", port))
	if connection != nil {
		connection.Close()
	}
	select {
	case <-started:
	default:
		t.Fatal("test did not hold the IPv6 primary attempt")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("dialer did not cancel the held IPv6 attempt")
	}
	if dialErr != nil {
		t.Fatalf("IPv4 fallback did not connect before the release deadline: %v", dialErr)
	}
}

// The resolver reads only synthetic loopback answers through an in-memory DNS connection.
func releaseTestDNSConnection(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	if deadline, ok := ctx.Deadline(); ok {
		server.SetDeadline(deadline)
	}
	go func() {
		defer server.Close()
		var size [2]byte
		if _, err := io.ReadFull(server, size[:]); err != nil {
			return
		}
		query := make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(server, query); err != nil || len(query) < 17 {
			return
		}
		end := 12
		for end < len(query) && query[end] != 0 {
			end += int(query[end]) + 1
		}
		end++
		if end+4 > len(query) {
			return
		}
		kind := binary.BigEndian.Uint16(query[end : end+2])
		address := net.IPv4(127, 0, 0, 1).To4()
		if kind == 28 {
			address = net.IPv6loopback
		} else if kind != 1 {
			return
		}
		answer := []byte{query[0], query[1], 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0}
		answer = append(answer, query[12:end+4]...)
		answer = append(answer, 0xc0, 0x0c, byte(kind>>8), byte(kind), 0, 1, 0, 0, 0, 0, 0, byte(len(address)))
		answer = append(answer, address...)
		binary.BigEndian.PutUint16(size[:], uint16(len(answer)))
		server.Write(append(size[:], answer...))
	}()
	return client, nil
}

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
