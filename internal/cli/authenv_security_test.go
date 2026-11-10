package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rojcarranza/bam/internal/provider/bamboo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingServer records the Authorization header of every request it
// receives, so a test can prove a server was never sent a stray token.
type recordingServer struct {
	mu    sync.Mutex
	auths []string
}

func (r *recordingServer) handler(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.auths = append(r.auths, req.Header.Get("Authorization"))
	r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"projects":{"size":0,"start-index":0,"max-result":25,"project":[]}}`))
}

func newRecordingServer(t *testing.T) (*httptest.Server, *recordingServer) {
	t.Helper()
	rs := &recordingServer{}
	srv := httptest.NewServer(http.HandlerFunc(rs.handler))
	t.Cleanup(srv.Close)
	return srv, rs
}

// realBackendHarness is like newHarness but wires Connect to the real Bamboo
// client (bamboo.New) instead of the fake backend, and lets the caller
// control both config files from scratch.
func realBackendHarness(t *testing.T, projectYAML, machineYAML string) *harness {
	t.Helper()
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(projectYAML), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte(machineYAML), 0o644))
	h.env.Connect = func(o bamboo.Options) (Backend, error) {
		h.connectOpts = append(h.connectOpts, o)
		c, err := bamboo.New(o)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	return h
}

// TestAuthEnvNeverCrossesOrigin reproduces the critical finding: a cloned
// repository's committed .bam.yaml (or a machine auth_env with no url of its
// own) must never cause a stored/env token to be sent to a host it was not
// created for. It uses the real Bamboo client against a real httptest server
// so the Authorization header actually sent (if any) can be inspected.
func TestAuthEnvNeverCrossesOrigin(t *testing.T) {
	srvB, recB := newRecordingServer(t)

	t.Run("project file auth_env is rejected before any request is made", func(t *testing.T) {
		h := realBackendHarness(t,
			"version: 1\nservers:\n  evil:\n    url: "+srvB.URL+"\n    auth_env: SOME_OTHER_SECRET\ndefault_server: evil\n",
			"version: 1\n")
		h.vars["SOME_OTHER_SECRET"] = "leaked-token-1"

		code := h.run("project", "list")
		assert.Equal(t, 3, code, h.stderr.String())
		assert.Contains(t, h.stderr.String(), "auth_env")
		assert.Empty(t, recB.auths, "server B must receive no request at all")
	})

	t.Run("machine auth_env without url never attaches to a project-defined url", func(t *testing.T) {
		h := realBackendHarness(t,
			"version: 1\nservers:\n  work:\n    url: "+srvB.URL+"\ndefault_server: work\n",
			"version: 1\nservers:\n  work:\n    auth_env: BAM_WORK_TOKEN\n")
		h.vars["BAM_WORK_TOKEN"] = "leaked-token-2"

		code := h.run("project", "list")
		assert.Equal(t, 4, code, h.stderr.String()) // no token available for server B's origin
		for _, a := range recB.auths {
			assert.NotEqual(t, "Bearer leaked-token-2", a)
		}
	})

	// Across both cases server B received nothing at all: the bug this
	// guards against would have sent it "Bearer leaked-token-1" or
	// "Bearer leaked-token-2".
	assert.Empty(t, recB.auths)
}
