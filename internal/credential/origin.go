// Package credential finds, stores and deletes Bamboo tokens. Tokens are
// keyed by server origin so a token is never sent to a host it was not
// created for.
package credential

import (
	"net"
	"net/url"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
)

// Origin normalises a server URL to scheme://host[:port].
func Origin(raw string) (string, error) {
	bad := func() error {
		return errs.Configf("invalid server URL %q", raw).WithTry("use a full URL such as https://bamboo.example.com")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", bad()
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", bad()
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", bad()
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host, nil
}
