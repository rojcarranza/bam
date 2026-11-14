package readonly_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/bamboo"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/rojcarranza/bam/internal/provider/readonly"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every Provider method is classified, so a method added later (BAM-40's
// agent switch) cannot reach Bamboo as an unguarded write.
func TestEveryProviderMethodIsClassified(t *testing.T) {
	typ := reflect.TypeOf((*provider.Provider)(nil)).Elem()
	var all []string
	for i := range typ.NumMethod() {
		all = append(all, typ.Method(i).Name)
	}
	known := slices.Concat(readonly.Reads, readonly.Writes)
	assert.ElementsMatch(t, all, known, "classify each new Provider method in readonly.Reads or readonly.Writes")
}

func TestWriteMethodsAreRefusedAndReadsPass(t *testing.T) {
	f := &fake.Provider{BaseURL: "https://bamboo.example.com", User: provider.User{Name: "jdoe"}}
	p := readonly.Wrap(f)

	_, err := p.Trigger(t.Context(), provider.TriggerRequest{PlanKey: "PROJ-BUILD"})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Contains(t, e.What, "read-only mode")
	assert.NotEmpty(t, e.Why)
	require.Error(t, p.StopBuild(t.Context(), "PROJ-BUILD-1"))
	_, err = p.RestartBuild(t.Context(), "PROJ-BUILD-1")
	require.Error(t, err)
	assert.Empty(t, f.Triggered)
	assert.Empty(t, f.Stopped)
	assert.Empty(t, f.Restarted)

	u, err := p.CurrentUser(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "jdoe", u.Name)
	_, ok := p.(provider.PlanSearcher)
	assert.True(t, ok, "optional extensions stay visible")
}

// Against the real Bamboo client and an HTTP server: of the calls a command
// can make, only GETs leave bam.
func TestOnlyGETsReachTheServer(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := bamboo.New(bamboo.Options{BaseURL: srv.URL, Token: "tok"})
	require.NoError(t, err)
	p := readonly.Wrap(c)
	_, _ = p.Trigger(t.Context(), provider.TriggerRequest{PlanKey: "PROJ-BUILD"})
	_ = p.StopBuild(t.Context(), "PROJ-BUILD-1")
	_, _ = p.RestartBuild(t.Context(), "PROJ-BUILD-1")
	_, _ = p.GetBuild(t.Context(), "PROJ-BUILD-1")
	mu.Lock()
	defer mu.Unlock()
	assert.NotEmpty(t, methods, "the read went out")
	for _, m := range methods {
		assert.Equal(t, http.MethodGet, m)
	}
}
