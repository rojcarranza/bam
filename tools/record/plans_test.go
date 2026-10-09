package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthPlansKeepsPagingAndHidesPlans(t *testing.T) {
	ids := map[string]int{}
	body := []byte(`{"key":"PROJ","name":"Example Project","description":"secret project","link":{"href":"x"},
		"plans":{"size":25,"start-index":0,"max-result":25,"expand":"plan","plan":[
		{"key":"PROJ-DEPLOY","shortKey":"DEPLOY","name":"Example Project - Deploy prod","shortName":"Deploy prod","projectKey":"PROJ","enabled":true,"description":"internal"},
		{"key":"PROJ-TEST","shortKey":"TEST","name":"Example Project - Test","shortName":"Test","projectKey":"PROJ","enabled":false}]}}`)
	out, err := synthPlans(body, ids, "PROJ")
	require.NoError(t, err)

	s := string(out)
	for _, leak := range []string{"DEPLOY", "Deploy prod", "secret project", "internal", "href"} {
		assert.NotContains(t, s, leak)
	}
	var got struct {
		Key   string `json:"key"`
		Plans struct {
			Size       int    `json:"size"`
			StartIndex int    `json:"start-index"`
			MaxResult  int    `json:"max-result"`
			Expand     string `json:"expand"`
			Plan       []struct {
				Key, ShortKey, Name, ShortName, ProjectKey string
				Enabled                                    bool
			} `json:"plan"`
		} `json:"plans"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, "PROJ", got.Key)
	assert.Equal(t, 25, got.Plans.Size)
	assert.Equal(t, 25, got.Plans.MaxResult)
	assert.Equal(t, "plan", got.Plans.Expand)
	require.Len(t, got.Plans.Plan, 2)
	assert.Equal(t, "PROJ-P1", got.Plans.Plan[0].Key)
	assert.Equal(t, "P1", got.Plans.Plan[0].ShortKey)
	assert.Equal(t, "Plan 1", got.Plans.Plan[0].ShortName)
	assert.Equal(t, "PROJ - Plan 1", got.Plans.Plan[0].Name)
	assert.Equal(t, "PROJ", got.Plans.Plan[0].ProjectKey)
	assert.False(t, got.Plans.Plan[1].Enabled)

	// The same real plan gets the same number in a later page, so a server
	// that ignores the range shows up as repeated numbers.
	again, err := synthPlans([]byte(`{"plans":{"size":1,"plan":[{"key":"PROJ-TEST","projectKey":"PROJ"}]}}`), ids, "PROJ")
	require.NoError(t, err)
	assert.Contains(t, string(again), `"PROJ-P2"`)
}

func TestSynthPlansHidesOtherProjects(t *testing.T) {
	// GET plan lists every project's plans, including projects the
	// scrubber has no placeholder for.
	out, err := synthPlans([]byte(`{"plans":{"size":2,"plan":[
		{"key":"PROJ-BUILD","projectKey":"PROJ"},
		{"key":"UNMAPPED-DEPLOY","projectKey":"UNMAPPED"}]}}`), map[string]int{}, "PROJ")
	require.NoError(t, err)
	assert.NotContains(t, string(out), "UNMAPPED")
	assert.Contains(t, string(out), `"PROJ-P1"`)
	assert.Contains(t, string(out), `"OPS-P2"`)
}

func TestProbePlansRecordsBothPagesAndTheFallback(t *testing.T) {
	// The server reports size as the page count and ignores the range.
	r, calls := methodServer(t, func(w http.ResponseWriter, req *http.Request) {
		var plans []string
		for i := range 25 {
			plans = append(plans, fmt.Sprintf(`{"key":"PROJ-REAL%d","name":"Example Project - Real %d","projectKey":"PROJ"}`, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"key":"PROJ","name":"Example Project","plans":{"size":25,"start-index":0,"max-result":25,"plan":[%s]}}`, join(plans))
	})
	require.NoError(t, r.probePlans("PROJ"))

	assert.Equal(t, []string{
		"GET /rest/api/latest/project/PROJ",
		"GET /rest/api/latest/project/PROJ",
		"GET /rest/api/latest/plan",
	}, calls())
	for _, f := range []string{"plans_page1.json", "plans_page2.json", "plans_all_page2.json"} {
		b, err := os.ReadFile(filepath.Join(r.out, f))
		require.NoError(t, err, f)
		assert.NotContains(t, string(b), "REAL", f)
		assert.Contains(t, string(b), `"PROJ-P1"`, f)
	}
}

func TestProbePlansQueries(t *testing.T) {
	var queries []string
	r, _ := methodServer(t, func(w http.ResponseWriter, req *http.Request) {
		queries = append(queries, req.URL.Path+"?"+req.URL.RawQuery)
		_, _ = w.Write([]byte(`{"plans":{"size":0,"plan":[]}}`))
	})
	require.NoError(t, r.probePlans("PROJ"))
	assert.Equal(t, "/rest/api/latest/project/PROJ?expand=plans.plan", queries[0])
	assert.True(t, strings.HasSuffix(queries[1], "expand=plans%5B25%3A125%5D.plan"), queries[1])
	assert.Equal(t, "/rest/api/latest/plan?max-result=25&start-index=25", queries[2])
}
