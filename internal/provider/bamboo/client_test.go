package bamboo

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestsCarryAuthAndAccept(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/info": {body: `{"version":"9.6.2"}`}})
	var out struct{ Version string }
	require.NoError(t, c.getJSON(context.Background(), "/rest/api/latest/info", nil, &out))
	assert.Equal(t, "9.6.2", out.Version)
	r := rec.all()[0]
	assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
	assert.Equal(t, "application/json", r.Header.Get("Accept"))
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   errs.Kind
		text   string
	}{
		{401, ``, errs.KindAuth, "401"},
		{403, ``, errs.KindAuth, "403"},
		{404, ``, errs.KindBamboo, "not found"},
		{400, `{"message":"Branch does not exist"}`, errs.KindBamboo, "Branch does not exist"},
		{400, `{"errors":["first problem"]}`, errs.KindBamboo, "first problem"},
		{500, `oops`, errs.KindBamboo, "500"},
	}
	for _, tc := range cases {
		c, _ := newTestServer(t, map[string]*route{"GET /x": {status: tc.status, body: tc.body}})
		_, err := c.do(context.Background(), request{method: "GET", path: "/x"})
		require.Error(t, err, tc.status)
		assert.Equal(t, tc.kind, errs.KindOf(err), tc.status)
		assert.Contains(t, err.Error(), tc.text, tc.status)
	}
}

func TestNotFoundWrapsSentinel(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	_, err := c.do(context.Background(), request{method: "GET", path: "/missing"})
	assert.True(t, errors.Is(err, errs.ErrNotFound))
}

func TestRetriesOn5xxAnd429Only(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /flaky": {status: 503, times: 2, then: &route{body: `{}`}},
		"GET /busy":  {status: 429, header: map[string]string{"Retry-After": "1"}, times: 1, then: &route{body: `{}`}},
		"GET /bad":   {status: 400, body: `{}`},
		"GET /down":  {status: 500},
	})
	ctx := context.Background()
	_, err := c.do(ctx, request{method: "GET", path: "/flaky"})
	require.NoError(t, err)
	_, err = c.do(ctx, request{method: "GET", path: "/busy"})
	require.NoError(t, err)
	_, err = c.do(ctx, request{method: "GET", path: "/bad"})
	require.Error(t, err)
	_, err = c.do(ctx, request{method: "GET", path: "/down"})
	require.Error(t, err)

	count := map[string]int{}
	for _, r := range rec.all() {
		count[r.URL.Path]++
	}
	assert.Equal(t, 3, count["/flaky"])
	assert.Equal(t, 2, count["/busy"])
	assert.Equal(t, 1, count["/bad"], "4xx is never retried")
	assert.Equal(t, 3, count["/down"], "three attempts in total")
}

func TestNonGetIsRetriedOnlyOn429Not5xx(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"POST /trigger": {status: 502},
		"POST /queue":   {status: 429, header: map[string]string{"Retry-After": "0"}, times: 1, then: &route{body: `{}`}},
	})
	ctx := context.Background()

	_, err := c.do(ctx, request{method: "POST", path: "/trigger"})
	require.Error(t, err)

	_, err = c.do(ctx, request{method: "POST", path: "/queue"})
	require.NoError(t, err)

	count := map[string]int{}
	for _, r := range rec.all() {
		count[r.URL.Path]++
	}
	assert.Equal(t, 1, count["/trigger"], "a POST must never be retried on 5xx: it could queue a duplicate build")
	assert.Equal(t, 2, count["/queue"], "a POST is still retried on 429")
}

func TestCanceledContextIsReturnedAsIs(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /x": {body: `{}`}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.do(ctx, request{method: "GET", path: "/x"})
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestRedirectToAnotherOriginIsRefused(t *testing.T) {
	var mu sync.Mutex
	var otherHits int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		otherHits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)

	c, _ := newTestServer(t, map[string]*route{
		"GET /x": {status: http.StatusFound, header: map[string]string{"Location": other.URL + "/y"}},
	})
	_, err := c.do(context.Background(), request{method: "GET", path: "/x"})
	require.Error(t, err)
	assert.Equal(t, errs.KindBamboo, errs.KindOf(err))
	assert.Contains(t, err.Error(), "does not follow redirects to another origin")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Try, "final URL")
	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, otherHits, "the redirect target must never receive a request")
}

func TestSameOriginRedirectIsFollowed(t *testing.T) {
	routes := map[string]*route{
		"GET /y": {body: `{"ok":true}`},
	}
	c, rec := newTestServer(t, routes)
	routes["GET /x"] = &route{status: http.StatusFound, header: map[string]string{"Location": c.base.String() + "/y"}}

	_, err := c.do(context.Background(), request{method: "GET", path: "/x"})
	require.NoError(t, err)
	paths := map[string]int{}
	for _, r := range rec.all() {
		paths[r.URL.Path]++
	}
	assert.Equal(t, 1, paths["/x"])
	assert.Equal(t, 1, paths["/y"])
}

func TestInjectedHTTPClientIsNotMutated(t *testing.T) {
	injected := &http.Client{}
	_, err := New(Options{BaseURL: "http://127.0.0.1:1", Token: "t", HTTP: injected})
	require.NoError(t, err)
	assert.Nil(t, injected.CheckRedirect, "New must clone an injected client, never mutate the caller's")
}

func TestUnreachableServerIsBambooError(t *testing.T) {
	c, err := New(Options{BaseURL: "http://127.0.0.1:1", Token: "t", Sleep: func(context.Context, time.Duration) error { return nil }})
	require.NoError(t, err)
	_, err = c.do(context.Background(), request{method: "GET", path: "/x"})
	require.Error(t, err)
	assert.Equal(t, errs.KindBamboo, errs.KindOf(err))
	assert.Contains(t, err.Error(), "cannot reach")
}

func TestDebugLogRedactsSecretQueryValues(t *testing.T) {
	var buf bytes.Buffer
	c, _ := newTestServer(t, map[string]*route{"POST /q": {body: `{}`}})
	c.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	q := url.Values{"bamboo.variable.db_password": {"hunter2"}, "bamboo.variable.env": {"staging"}}
	_, err := c.do(context.Background(), request{method: "POST", path: "/q", query: q,
		secret: map[string]bool{"bamboo.variable.db_password": true}})
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "hunter2")
	assert.NotContains(t, buf.String(), "test-token")
	assert.Contains(t, buf.String(), "staging")
	assert.Contains(t, buf.String(), "status=200")
}

func TestBackoffCapsRetryAfterAt60s(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	assert.Equal(t, 60*time.Second, c.backoff(1, "3600"), "a large Retry-After is capped at 60s")
	assert.Equal(t, 5*time.Second, c.backoff(1, "5"), "a small Retry-After is kept as is")
}

func TestPageAllFollowsStartIndex(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/project": {body: `{"projects":{"size":3,"start-index":0,"max-result":2,"project":[{"key":"PROJ"},{"key":"OPS"}]}}`,
			times: 1, then: &route{body: `{"projects":{"size":3,"start-index":2,"max-result":2,"project":[{"key":"LAB"}]}}`}},
	})
	type p struct{ Key string }
	got, err := pageAll[p](context.Background(), c, "/rest/api/latest/project", nil, "projects", 0)
	require.NoError(t, err)
	assert.Equal(t, []p{{"PROJ"}, {"OPS"}, {"LAB"}}, got)
	assert.Equal(t, "2", rec.all()[1].URL.Query().Get("start-index"))
}

func TestStatusOf(t *testing.T) {
	assert.Equal(t, 0, statusOf(errors.New("boom")))
	c, _ := newTestServer(t, map[string]*route{"GET /x": {status: 404}})
	_, err := c.do(context.Background(), request{method: "GET", path: "/x"})
	assert.Equal(t, 404, statusOf(err))
}

func TestPageAllStopsAtLimit(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /r": {body: `{"results":{"size":50,"start-index":0,"max-result":2,"result":[{"key":"A"},{"key":"B"}]}}`},
	})
	type p struct{ Key string }
	got, err := pageAll[p](context.Background(), c, "/r", nil, "results", 2)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Len(t, rec.all(), 1)
	assert.Equal(t, "2", rec.all()[0].URL.Query().Get("max-result"))
}

func TestErrorTextAndDebugBodyRedactSecretValues(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		body   string
		user   string
		debug  string
		reason string
	}{
		{
			name:   "raw",
			value:  "hunter2",
			body:   `{"message":"rejected value hunter2 for bamboo.variable.db_password"}`,
			user:   "hunter2",
			debug:  "hunter2",
			reason: "Bamboo's raw echo of a secret value must not reach the user",
		},
		{
			name:   "html escaped",
			value:  "hunter&2",
			body:   `{"message":"rejected value hunter&amp;2 for bamboo.variable.db_password"}`,
			user:   "hunter&amp;2",
			debug:  "hunter&amp;2",
			reason: "Bamboo's HTML-escaped echo of a secret value must not reach the user",
		},
		{
			name:   "json escaped",
			value:  `a"b<c`,
			body:   `{"message":"rejected value a\"b\u003cc for bamboo.variable.db_password"}`,
			user:   `a"b<c`,
			debug:  `a\"b\u003cc`,
			reason: "Bamboo's JSON-escaped echo of a secret value must not reach debug output",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			c, _ := newTestServer(t, map[string]*route{
				"POST /q": {status: 400, body: tc.body},
			})
			c.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			form := url.Values{"bamboo.variable.db_password": {tc.value}, "bamboo.variable.env": {"staging"}}
			_, err := c.do(context.Background(), request{method: "POST", path: "/q", form: form,
				secret: map[string]bool{"bamboo.variable.db_password": true}})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), tc.user, tc.reason)
			assert.Contains(t, err.Error(), MaskedValue)
			assert.NotContains(t, buf.String(), tc.debug, "--debug must not log an echoed secret value")
		})
	}
}

func TestErrorTextAndDebugBodyRedactOverlappingSecretValues(t *testing.T) {
	var buf bytes.Buffer
	c, _ := newTestServer(t, map[string]*route{
		"POST /q": {status: 400, body: `{"message":"rejected values password and pass for trigger"}`},
	})
	c.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	form := url.Values{
		"bamboo.variable.first":  {"pass"},
		"bamboo.variable.second": {"password"},
	}
	_, err := c.do(context.Background(), request{method: "POST", path: "/q", form: form,
		secret: map[string]bool{
			"bamboo.variable.first":  true,
			"bamboo.variable.second": true,
		}})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "password")
	assert.NotContains(t, err.Error(), "pass")
	assert.NotContains(t, err.Error(), "********word")
	assert.NotContains(t, buf.String(), "password")
	assert.NotContains(t, buf.String(), "pass")
	assert.NotContains(t, buf.String(), "********word")
}
