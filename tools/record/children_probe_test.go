package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// methodServer answers routes by path and records every request's method.
func methodServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*recorder, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method+" "+r.URL.Path)
		mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return &recorder{base: srv.URL, token: "test-token", http: srv.Client(), out: t.TempDir()}, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}

func TestProbeChildrenRecordsTheChildAndOnlyReads(t *testing.T) {
	routes := map[string]string{
		"/rest/api/latest/application.wadl":                    dependencyWADL,
		"/rest/api/latest/plan/LAB-PARENT":                     `{"key":"LAB-PARENT"}`,
		"/rest/api/latest/dependency/search/LAB-PARENT/child":  `{"dependencies":{"plan":[]}}`,
		"/rest/api/latest/dependency/search/LAB-PARENT/parent": `{"dependencies":{"plan":[]}}`,
		"/rest/api/latest/result":                              `{"results":{"result":[]}}`,
		"/rest/api/latest/result/LAB-PARENT":                   `{"results":{"result":[{"key":"LAB-PARENT-2","buildResultKey":"LAB-PARENT-2","buildReason":"Manual run by jdoe"}]}}`,
		"/rest/api/latest/result/LAB-PARENT-2":                 `{"key":"LAB-PARENT-2"}`,
		"/rest/api/latest/plan":                                `{"plans":{"size":3,"start-index":0,"plan":[{"key":"LAB-PARENT"},{"key":"LAB-CHILD"},{"key":"OPS-OTHER"}]}}`,
		"/rest/api/latest/result/LAB-CHILD": `{"results":{"result":[{"buildResultKey":"LAB-CHILD-1",` +
			`"buildReason":"Child of <a href=\"http://bamboo.example.com/browse/LAB-PARENT-2\">LAB-PARENT-2</a>"}]}}`,
		"/rest/api/latest/result/OPS-OTHER":   `{"results":{"result":[{"buildResultKey":"OPS-OTHER-4","buildReason":"Manual run by jdoe"}]}}`,
		"/rest/api/latest/result/LAB-CHILD-1": `{"key":"LAB-CHILD-1"}`,
	}
	r, methods := methodServer(t, func(w http.ResponseWriter, req *http.Request) {
		body, ok := routes[req.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	require.NoError(t, r.probeChildren("LAB-PARENT"))
	for _, m := range methods() {
		assert.Regexp(t, `^GET `, m, "the probe only reads")
	}
	child, err := os.ReadFile(filepath.Join(r.out, "children_child_0.json"))
	require.NoError(t, err)
	assert.Contains(t, string(child), "LAB-CHILD-1")
	for _, f := range []string{"children_wadl.txt", "children_plan.json", "children_parent_result.json",
		"children_dependency_child.json", "children_dependency_child_other.json", "children_plan_dependencies.json.status"} {
		_, err := os.Stat(filepath.Join(r.out, f))
		assert.NoError(t, err, f)
	}
	_, err = os.Stat(filepath.Join(r.out, "children_child_1.json"))
	assert.True(t, os.IsNotExist(err), "OPS-OTHER-4 is not a child")
}

func TestPlanKeysFollowsPages(t *testing.T) {
	r, _ := methodServer(t, func(w http.ResponseWriter, req *http.Request) {
		start, _ := strconv.Atoi(req.URL.Query().Get("start-index"))
		max, _ := strconv.Atoi(req.URL.Query().Get("max-result"))
		max = min(max, 100) // the server caps a page, whatever is asked for
		var plans []string
		for i := start; i < 250 && i < start+max; i++ {
			plans = append(plans, fmt.Sprintf(`{"key":"LAB-P%d"}`, i))
		}
		_, _ = fmt.Fprintf(w, `{"plans":{"size":250,"start-index":%d,"max-result":%d,"plan":[%s]}}`, start, max, join(plans))
	})
	keys, err := r.planKeys()
	require.NoError(t, err)
	require.Len(t, keys, 250)
	assert.Equal(t, "LAB-P249", keys[249])
}

func join(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
