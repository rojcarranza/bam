package bamboo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListPlansPagesPastTheFirst25 replays how Bamboo 9.6 pages a project's
// plans (issue #43, recorded against a lab project of 30): the expand range
// plans[25:125].plan answers size 30 and no plans at all, while start-index
// and max-result page as they do everywhere else. This server also caps
// max-result at 25, so a project of 60 takes three pages.
func TestListPlansPagesPastTheFirst25(t *testing.T) {
	const total = 60
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rest/api/latest/result/") {
			_, _ = w.Write([]byte(`{"results":{"size":0,"result":[]}}`))
			return
		}
		if r.URL.Path != "/rest/api/latest/project/PROJ" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		start, _ := strconv.Atoi(q.Get("start-index"))
		max := 25
		if n, err := strconv.Atoi(q.Get("max-result")); err == nil && n < max {
			max = n
		}
		if q.Get("expand") != "plans.plan" {
			max = 0 // the expand range: right size, no plans
		}
		starts = append(starts, q.Get("start-index"))
		var items []string
		for i := start; i < min(start+max, total); i++ {
			items = append(items, fmt.Sprintf(`{"key":"PROJ-P%d","shortName":"Plan %d"}`, i+1, i+1))
		}
		fmt.Fprintf(w, `{"key":"PROJ","name":"Example Project","plans":{"size":%d,"start-index":%d,"max-result":%d,"expand":"plan","plan":[%s]}}`,
			total, start, len(items), strings.Join(items, ","))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "test-token", Now: time.Now})
	require.NoError(t, err)

	plans, err := c.ListPlans(ctx, "PROJ")
	require.NoError(t, err)
	require.Len(t, plans, total)
	seen := map[string]bool{}
	for _, p := range plans {
		assert.False(t, seen[p.Key], "duplicate %s", p.Key)
		seen[p.Key] = true
	}
	assert.Equal(t, "PROJ-P60", plans[59].Key)
	assert.Equal(t, []string{"0", "25", "50"}, starts)
}

// TestListPlansServerIgnoringStartIndex: a server that answers every page
// with the first one ends the listing without repeating a plan.
func TestListPlansServerIgnoringStartIndex(t *testing.T) {
	page := make([]string, 25)
	for i := range page {
		page[i] = fmt.Sprintf(`{"key":"PROJ-P%d","shortName":"Plan %d"}`, i+1, i+1)
	}
	body := `{"key":"PROJ","plans":{"size":30,"start-index":0,"max-result":25,"plan":[` + strings.Join(page, ",") + `]}}`
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rest/api/latest/result/") {
			_, _ = w.Write([]byte(`{"results":{"size":0,"result":[]}}`))
			return
		}
		n++
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "test-token", Now: time.Now})
	require.NoError(t, err)

	plans, err := c.ListPlans(ctx, "PROJ")
	require.NoError(t, err)
	require.Len(t, plans, 25)
	assert.Equal(t, 2, n, "stops once a page brings no new plan")
}

// TestListPlansSizeAsPageCount: a server that reports size as the page's
// count, not the total, still gets asked for the next page until one comes
// back empty.
func TestListPlansSizeAsPageCount(t *testing.T) {
	const total = 60
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rest/api/latest/result/") {
			_, _ = w.Write([]byte(`{"results":{"size":0,"result":[]}}`))
			return
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("start-index"))
		starts = append(starts, r.URL.Query().Get("start-index"))
		var items []string
		for i := start; i < min(start+25, total); i++ {
			items = append(items, fmt.Sprintf(`{"key":"PROJ-P%d"}`, i+1))
		}
		fmt.Fprintf(w, `{"plans":{"size":%d,"start-index":%d,"plan":[%s]}}`, len(items), start, strings.Join(items, ","))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "test-token", Now: time.Now})
	require.NoError(t, err)

	plans, err := c.ListPlans(ctx, "PROJ")
	require.NoError(t, err)
	require.Len(t, plans, total)
	assert.Equal(t, []string{"0", "25", "50", "60"}, starts)
}

// TestListPlansReplaysTheRecordedPages serves the probe's recording of a
// 30-plan lab project (make record ARGS='-probe-plans ...'): page 1 by
// expand alone, page 2 by start-index. The recorded expand range page holds
// no plans, which is the shape that cut the list at 25.
func TestListPlansReplaysTheRecordedPages(t *testing.T) {
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "recorded", name))
		require.NoError(t, err)
		return b
	}
	var empty struct {
		Plans struct {
			Size int               `json:"size"`
			Plan []json.RawMessage `json:"plan"`
		} `json:"plans"`
	}
	require.NoError(t, json.Unmarshal(read("plans_page2.json"), &empty))
	require.Equal(t, 30, empty.Plans.Size)
	require.Empty(t, empty.Plans.Plan, "Bamboo answers the expand range with no plans")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/api/latest/result/"):
			_, _ = w.Write([]byte(`{"results":{"size":0,"result":[]}}`))
		case r.URL.Query().Get("start-index") == "25":
			_, _ = w.Write(read("plans_page2_index.json"))
		case r.URL.Query().Get("start-index") == "0":
			_, _ = w.Write(read("plans_page1.json"))
		default:
			_, _ = w.Write(read("plans_page2.json"))
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "test-token", Now: time.Now})
	require.NoError(t, err)

	plans, err := c.ListPlans(ctx, "LAB")
	require.NoError(t, err)
	require.Len(t, plans, 30)
	assert.Equal(t, "Plan 26", plans[25].Name)
}
