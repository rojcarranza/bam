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
	body := []byte(`{"key":"WORK","name":"Work","description":"secret project","link":{"href":"x"},
		"plans":{"size":25,"start-index":0,"max-result":25,"expand":"plan","plan":[
		{"key":"WORK-DEPLOY","shortKey":"DEPLOY","name":"Work - Deploy prod","shortName":"Deploy prod","projectKey":"WORK","enabled":true,"description":"internal"},
		{"key":"WORK-TEST","shortKey":"TEST","name":"Work - Test","shortName":"Test","projectKey":"WORK","enabled":false}]}}`)
	out, err := synthPlans(body, ids)
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
	assert.Equal(t, "WORK", got.Key)
	assert.Equal(t, 25, got.Plans.Size)
	assert.Equal(t, 25, got.Plans.MaxResult)
	assert.Equal(t, "plan", got.Plans.Expand)
	require.Len(t, got.Plans.Plan, 2)
	assert.Equal(t, "WORK-P1", got.Plans.Plan[0].Key)
	assert.Equal(t, "P1", got.Plans.Plan[0].ShortKey)
	assert.Equal(t, "Plan 1", got.Plans.Plan[0].ShortName)
	assert.Equal(t, "WORK - Plan 1", got.Plans.Plan[0].Name)
	assert.Equal(t, "WORK", got.Plans.Plan[0].ProjectKey)
	assert.False(t, got.Plans.Plan[1].Enabled)

	// The same real plan gets the same number in a later page, so a server
	// that ignores the range shows up as repeated numbers.
	again, err := synthPlans([]byte(`{"plans":{"size":1,"plan":[{"key":"WORK-TEST","projectKey":"WORK"}]}}`), ids)
	require.NoError(t, err)
	assert.Contains(t, string(again), `"WORK-P2"`)
}

func TestProbePlansRecordsBothPagesAndTheFallback(t *testing.T) {
	// The server reports size as the page count and ignores the range.
	r, calls := methodServer(t, func(w http.ResponseWriter, req *http.Request) {
		var plans []string
		for i := range 25 {
			plans = append(plans, fmt.Sprintf(`{"key":"WORK-REAL%d","name":"Work - Real %d","projectKey":"WORK"}`, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"key":"WORK","name":"Work","plans":{"size":25,"start-index":0,"max-result":25,"plan":[%s]}}`, join(plans))
	})
	require.NoError(t, r.probePlans("WORK"))

	assert.Equal(t, []string{
		"GET /rest/api/latest/project/WORK",
		"GET /rest/api/latest/project/WORK",
		"GET /rest/api/latest/plan",
	}, calls())
	for _, f := range []string{"plans_page1.json", "plans_page2.json", "plans_all_page2.json"} {
		b, err := os.ReadFile(filepath.Join(r.out, f))
		require.NoError(t, err, f)
		assert.NotContains(t, string(b), "REAL", f)
		assert.Contains(t, string(b), `"WORK-P1"`, f)
	}
}

func TestProbePlansQueries(t *testing.T) {
	var queries []string
	r, _ := methodServer(t, func(w http.ResponseWriter, req *http.Request) {
		queries = append(queries, req.URL.Path+"?"+req.URL.RawQuery)
		_, _ = w.Write([]byte(`{"plans":{"size":0,"plan":[]}}`))
	})
	require.NoError(t, r.probePlans("WORK"))
	assert.Equal(t, "/rest/api/latest/project/WORK?expand=plans.plan", queries[0])
	assert.True(t, strings.HasSuffix(queries[1], "expand=plans%5B25%3A125%5D.plan"), queries[1])
	assert.Equal(t, "/rest/api/latest/plan?max-result=25&start-index=25", queries[2])
}
