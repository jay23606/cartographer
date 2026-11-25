package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// InsecureHTTPEnv is the environment variable that opts a run into cleartext
// http:// to a non-loopback host, for one-off and unattended use.
const InsecureHTTPEnv = "CARTOGRAPHER_ALLOW_INSECURE_HTTP"

// IsLoopbackHost reports whether host (no port, no brackets) is the loopback
// interface: "localhost" (case-insensitive, trailing dot ignored) or any IP
// for which net.ParseIP(...).IsLoopback() holds (127.0.0.0/8, ::1, IPv4-mapped).
// It parses the address instead of matching a prefix: "127.0.0.1.example.com"
// is a DNS name, not loopback.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckServerURL is the one gate every client request passes: the bearer token
// must not leave for a non-loopback http:// URL unless allowInsecure is set.
// https is always accepted; any other scheme, or an unparsable URL, is refused.
func CheckServerURL(rawURL string, allowInsecure bool) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("client: invalid server URL %q", rawURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if allowInsecure || IsLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("refusing %s: plain http:// to a non-loopback host sends the bearer token in clear and the responses are unauthenticated; "+
			"move the server behind https://, or accept the risk explicitly with `cartographer connect --allow-insecure-http` "+
			"(re-run connect with the same flags; it only sets the key) or %s=1; nothing was modified", u.Redacted(), InsecureHTTPEnv)
	default:
		return fmt.Errorf("client: unsupported server URL scheme %q in %s (use https://)", u.Scheme, u.Redacted())
	}
}

// RedirectPolicy is the http.Client CheckRedirect that keeps the gate in force
// across redirects: Go keeps Authorization on a same-host redirect, so an
// https:// endpoint answering 302 to http:// would otherwise leak the token.
// A redirect whose target fails CheckServerURL is refused.
func RedirectPolicy(allowInsecure bool) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		// https -> http is refused even to loopback and even with the opt-in:
		// a secure endpoint never legitimately sends the client back to cleartext.
		if len(via) > 0 && strings.EqualFold(via[len(via)-1].URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
			return fmt.Errorf("refusing redirect from %s to %s: https to http downgrade", via[len(via)-1].URL.Redacted(), req.URL.Redacted())
		}
		return CheckServerURL(req.URL.String(), allowInsecure)
	}
}

// serverCA is the extra trust of the configured server (server.ca_file, WP6):
// the system roots plus one private CA file, honoured for one host:port only.
var serverCA struct {
	sync.RWMutex
	host string
	pool *x509.CertPool
	rt   http.RoundTripper
}

// SetServerCA registers path, a PEM file, as an extra root for requests to the
// host:port of serverURL. The system roots stay trusted and no other host
// trusts the file. An empty path clears it. A file that cannot be read or holds
// no certificate is an error, never a silent fallback.
func SetServerCA(path, serverURL string) error {
	serverCA.Lock()
	defer serverCA.Unlock()
	serverCA.host, serverCA.pool, serverCA.rt = "", nil, nil
	if path == "" {
		return nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("client: ca_file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("client: ca_file %s holds no PEM certificate", path)
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("client: invalid server URL %q", serverURL)
	}
	serverCA.host, serverCA.pool = u.Host, pool
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	serverCA.rt = t
	return nil
}

// ServerRoots returns the pool to verify host (host:port) with, or nil when
// the system roots apply: mcpprobe's own dial path uses it.
func ServerRoots(host string) *x509.CertPool {
	serverCA.RLock()
	defer serverCA.RUnlock()
	if serverCA.pool != nil && strings.EqualFold(serverCA.host, host) {
		return serverCA.pool
	}
	return nil
}

// scopedTransport sends the registered server host through the transport that
// trusts its CA and every other host through http.DefaultTransport.
type scopedTransport struct{}

func (scopedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	serverCA.RLock()
	rt := serverCA.rt
	match := rt != nil && strings.EqualFold(serverCA.host, req.URL.Host)
	serverCA.RUnlock()
	if match {
		return rt.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// HTTPClient returns a copy of base whose redirects obey RedirectPolicy. A base
// with no Transport also trusts the server's CA file for that host (SetServerCA).
func HTTPClient(base *http.Client, allowInsecure bool) *http.Client {
	hc := *base
	hc.CheckRedirect = RedirectPolicy(allowInsecure)
	if hc.Transport == nil {
		hc.Transport = scopedTransport{}
	}
	return &hc
}
