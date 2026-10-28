package bamboo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
)

const maxAttempts = 3

// Options configures a Client. Zero values get sensible defaults.
type Options struct {
	BaseURL  string
	Token    string
	HTTP     *http.Client
	Logger   *slog.Logger
	Caps     Capabilities       // cached capabilities for this server
	SaveCaps func(Capabilities) // called when a capability is learned
	Now      func() time.Time
	Sleep    func(ctx context.Context, d time.Duration) error
}

// Client talks to one Bamboo server.
type Client struct {
	base     *url.URL
	token    string
	http     *http.Client
	log      *slog.Logger
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
	saveCaps func(Capabilities)

	mu             sync.Mutex
	caps           Capabilities
	versionChecked bool
	version        string // the Bamboo version ServerInfo last returned
}

// New returns a Client for the server at o.BaseURL.
func New(o Options) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(o.BaseURL, "/"))
	if err != nil || base.Host == "" {
		return nil, errs.Configf("invalid server URL %q", o.BaseURL)
	}
	c := &Client{base: base, token: o.Token, http: o.HTTP, log: o.Logger, now: o.Now, sleep: o.Sleep, saveCaps: o.SaveCaps, caps: o.Caps}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	} else {
		// Clone an injected client before touching CheckRedirect: it may be
		// shared by the caller (or by other tests), so New must never mutate it.
		cloned := *c.http
		c.http = &cloned
	}
	c.http.CheckRedirect = checkRedirectSameOrigin
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if c.saveCaps == nil {
		c.saveCaps = func(Capabilities) {}
	}
	return c, nil
}

// URL returns the browse URL of any Bamboo key.
func (c *Client) URL(key string) string { return c.base.String() + "/browse/" + key }

// crossOriginRedirectError reports a redirect to a different scheme://host:port.
// Go's http.Client keeps the Authorization header on same-host redirects, so
// following a cross-origin one would leak the bearer token to that host.
type crossOriginRedirectError struct{ from, to string }

func (e *crossOriginRedirectError) Error() string {
	return fmt.Sprintf("redirected from %s to %s; bam does not follow redirects to another origin", e.from, e.to)
}

func requestOrigin(u *url.URL) string { return u.Scheme + "://" + u.Host }

// checkRedirectSameOrigin is installed as http.Client.CheckRedirect. It
// allows same-origin redirects and refuses any redirect that crosses
// scheme, host or port.
func checkRedirectSameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	from, to := requestOrigin(via[0].URL), requestOrigin(req.URL)
	if from != to {
		return &crossOriginRedirectError{from: from, to: to}
	}
	return nil
}

type request struct {
	method string
	path   string
	query  url.Values
	form   url.Values      // sent as an application/x-www-form-urlencoded body
	secret map[string]bool // query or form keys whose values are redacted in logs
	raw    bool            // do not ask for JSON (log download)
}

// do sends r, retrying 429 and 5xx, and returns the body of a 2xx response.
func (c *Client) do(ctx context.Context, r request) ([]byte, error) {
	var lastStatus int
	var lastBody []byte
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, body, header, err := c.once(ctx, r)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var redir *crossOriginRedirectError
			if errors.As(err, &redir) {
				return nil, errs.Bamboof("%s", redir.Error()).
					WithTry("use the final URL in your server config").Wrap(err)
			}
			return nil, errs.Bamboof("cannot reach %s", c.base.Host).
				WithWhy("check the URL, your network and any VPN").Wrap(err)
		}
		if status >= 200 && status < 300 {
			return body, nil
		}
		lastStatus, lastBody = status, body
		// 429 is retried for every method (it honours Retry-After). A 5xx is
		// retried only for GET/HEAD: retrying a POST/PUT/DELETE on a server
		// error can queue a duplicate build or fire a trigger twice.
		getOrHead := r.method == http.MethodGet || r.method == http.MethodHead
		if status != http.StatusTooManyRequests && (status < 500 || !getOrHead) {
			break
		}
		if attempt < maxAttempts {
			if err := c.sleep(ctx, c.backoff(attempt, header.Get("Retry-After"))); err != nil {
				return nil, err
			}
		}
	}
	return nil, c.statusError(r, lastStatus, lastBody)
}

func (c *Client) once(ctx context.Context, r request) (int, []byte, http.Header, error) {
	u := *c.base
	u.Path = c.base.Path + r.path
	u.RawQuery = r.query.Encode()
	var body io.Reader
	if len(r.form) > 0 {
		body = strings.NewReader(r.form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if !r.raw {
		req.Header.Set("Accept", "application/json")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	start := c.now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Debug("http", "method", r.method, "url", redactURL(u, r.secret), "error", err.Error())
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	c.log.Debug("http", "method", r.method, "url", redactURL(u, r.secret), "status", resp.StatusCode, "duration", c.now().Sub(start))
	if resp.StatusCode >= 400 && len(data) > 0 {
		c.log.Debug("http body", "body", truncate(r.scrubSecrets(string(data)), 2048))
	}
	return resp.StatusCode, data, resp.Header, err
}

// maxRetryAfter caps how long a single retry waits on a server-supplied
// Retry-After, so a misbehaving or malicious response cannot stall a command
// for an unbounded time.
const maxRetryAfter = 60 * time.Second

func (c *Client) backoff(attempt int, retryAfter string) time.Duration {
	if s, err := strconv.Atoi(retryAfter); err == nil && s >= 0 {
		d := time.Duration(s) * time.Second
		if d > maxRetryAfter {
			d = maxRetryAfter
		}
		return d
	}
	base := 500 * time.Millisecond << (attempt - 1)
	return base + time.Duration(rand.Int64N(int64(250*time.Millisecond)))
}

// httpError carries the status code of a failed response. A 404 matches
// errs.ErrNotFound through errors.Is.
type httpError struct {
	Status  int
	Message string // Bamboo's own error text, when it sent one
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }
func (e *httpError) Is(target error) bool {
	return e.Status == http.StatusNotFound && target == errs.ErrNotFound
}

// statusOf returns the HTTP status behind err, or 0.
func statusOf(err error) int {
	var h *httpError
	if errors.As(err, &h) {
		return h.Status
	}
	return 0
}

func (c *Client) statusError(r request, status int, body []byte) error {
	host := c.base.Host
	cause := &httpError{Status: status, Message: bambooMessage(body)}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errs.Authf("%d from %s", status, host).
			WithWhy("the token may be expired, revoked, or lack permission").
			WithTry("bam doctor").Wrap(cause)
	case status == http.StatusNotFound:
		return errs.Bamboof("not found: %s", r.path).Wrap(cause)
	case status >= 500:
		return errs.Bamboof("Bamboo server error %d from %s", status, host).Wrap(cause)
	default:
		e := errs.Bamboof("Bamboo rejected the request (%d)", status).Wrap(cause)
		if msg := bambooMessage(body); msg != "" {
			_ = e.WithWhy(r.scrubSecrets(msg))
		}
		return e
	}
}

// bambooMessage extracts Bamboo's error text from {"message":...} or {"errors":[...]}.
func bambooMessage(body []byte) string {
	var m struct {
		Message string            `json:"message"`
		Errors  []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	if m.Message != "" {
		return m.Message
	}
	for _, raw := range m.Errors {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
	}
	return ""
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	body, err := c.do(ctx, request{method: http.MethodGet, path: path, query: q})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errs.Bamboof("unexpected response from %s", path).WithWhy("the body is not the JSON bam expects").Wrap(err)
	}
	return nil
}

// scrubSecrets replaces every submitted secret value in s with MaskedValue.
// Bamboo echoes a rejected variable back in its error text, so a response can
// carry a value bam promised never to print; both the user-facing reason and
// the --debug body go through here. The escaped form is replaced too, because
// Bamboo HTML-escapes the text it echoes.
func (r request) scrubSecrets(s string) string {
	if len(r.secret) == 0 || s == "" {
		return s
	}
	replacements := map[string]struct{}{}
	for k := range r.secret {
		for _, v := range append(append([]string(nil), r.form[k]...), r.query[k]...) {
			if v == "" {
				continue
			}
			replacements[v] = struct{}{}
			if e := html.EscapeString(v); e != v {
				replacements[e] = struct{}{}
			}
			if j, ok := jsonEscaped(v); ok && j != v {
				replacements[j] = struct{}{}
			}
		}
	}
	values := make([]string, 0, len(replacements))
	for v := range replacements {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	for _, v := range values {
		s = strings.ReplaceAll(s, v, MaskedValue)
	}
	return s
}

func jsonEscaped(s string) (string, bool) {
	b, err := json.Marshal(s)
	if err != nil || len(b) < 2 {
		return "", false
	}
	return string(b[1 : len(b)-1]), true
}

func redactURL(u url.URL, secret map[string]bool) string {
	if len(secret) == 0 || u.RawQuery == "" {
		return u.String()
	}
	q := u.Query()
	for k := range q {
		if secret[k] {
			q.Set(k, "********")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
