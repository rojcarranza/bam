package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/r0jjames/bam-cli/internal/toolcfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScrubVariableValuesReplacesEveryValueButMasked(t *testing.T) {
	in := []byte(`[{"name":"cluster_type","value":"k8s"},{"name":"compute_nodes","value":"2"},{"name":"db_password","value":"********"}]`)
	out, err := scrubVariableValues(in)
	require.NoError(t, err)

	var items []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(out, &items))
	require.Len(t, items, 3)
	assert.Equal(t, "value-1", items[0].Value)
	assert.Equal(t, "value-2", items[1].Value)
	assert.Equal(t, "********", items[2].Value, "a masked value is left as is")
}

func TestScrubVariableValuesHandlesNestedEnvelope(t *testing.T) {
	in := []byte(`{"key":"PROJ-BUILD-482","buildNumber":482,"variables":{"size":2,"start-index":0,"max-result":2,"variable":[` +
		`{"key":"cluster_type","value":"dcos"},{"key":"cluster_name","value":"beta"}]}}`)
	out, err := scrubVariableValues(in)
	require.NoError(t, err)

	var doc struct {
		Variables struct {
			Variable []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"variable"`
		} `json:"variables"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	require.Len(t, doc.Variables.Variable, 2)
	assert.Equal(t, "value-1", doc.Variables.Variable[0].Value)
	assert.Equal(t, "value-2", doc.Variables.Variable[1].Value)
	assert.Equal(t, "cluster_type", doc.Variables.Variable[0].Key, "name/key is untouched")
}

func TestScrubLogEntriesTruncatesAndReplacesText(t *testing.T) {
	in := []byte(`{"logEntries":{"size":7,"start-index":0,"max-result":50,"logEntry":[` +
		`{"date":1,"log":"one","unstyledLog":"one"},` +
		`{"date":2,"log":"two","unstyledLog":"two"},` +
		`{"date":3,"log":"three","unstyledLog":"three"},` +
		`{"date":4,"log":"four","unstyledLog":"four"},` +
		`{"date":5,"log":"five","unstyledLog":"five"},` +
		`{"date":6,"log":"six","unstyledLog":"six"},` +
		`{"date":7,"log":"seven","unstyledLog":"seven"}]}}`)
	out, err := scrubLogEntries(in)
	require.NoError(t, err)

	var doc struct {
		LogEntries struct {
			Size     int `json:"size"`
			LogEntry []struct {
				Log         string `json:"log"`
				UnstyledLog string `json:"unstyledLog"`
			} `json:"logEntry"`
		} `json:"logEntries"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	require.Len(t, doc.LogEntries.LogEntry, 5, "at most the first 5 entries are kept")
	assert.Equal(t, 5, doc.LogEntries.Size)
	for i, e := range doc.LogEntries.LogEntry {
		want := "log line " + string(rune('1'+i))
		assert.Equal(t, want, e.Log)
		assert.Equal(t, want, e.UnstyledLog)
	}
}

func TestScrubLogDownloadTruncatesAndReplacesMessage(t *testing.T) {
	in := []byte("simple\t01-Jan-2026 00:00:01\tfirst message\n" +
		"simple\t01-Jan-2026 00:00:02\tsecond message\n" +
		"simple\t01-Jan-2026 00:00:03\tthird message\n" +
		"simple\t01-Jan-2026 00:00:04\tfourth message\n" +
		"simple\t01-Jan-2026 00:00:05\tfifth message\n" +
		"simple\t01-Jan-2026 00:00:06\tsixth message\n")
	out := scrubLogDownload(in)
	lines := splitLines(string(out))
	require.Len(t, lines, 5, "at most the first 5 lines are kept")
	assert.Equal(t, "simple\t01-Jan-2026 00:00:01\tlog line 1", lines[0])
	assert.Equal(t, "simple\t01-Jan-2026 00:00:05\tlog line 5", lines[4])
}

// TestWriteScrubbedFailsClosedWhenScrubbingFails reproduces the bug where
// record() ignored the error from scrubVariableValues/scrubLogEntries and
// wrote the raw (unscrubbed) body anyway. writeScrubbed must instead return
// the error and write nothing for that file.
func TestWriteScrubbedFailsClosedWhenScrubbingFails(t *testing.T) {
	dir := t.TempDir()

	err := writeScrubbed(dir, "plan_variables.json", []byte("not json"), bamboo.Scrubber{})
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(dir, "plan_variables.json"))
	assert.True(t, os.IsNotExist(statErr), "no file should be written when scrubVariableValues fails")

	err = writeScrubbed(dir, "log_entries.json", []byte("not json"), bamboo.Scrubber{})
	require.Error(t, err)
	_, statErr = os.Stat(filepath.Join(dir, "log_entries.json"))
	assert.True(t, os.IsNotExist(statErr), "no file should be written when scrubLogEntries fails")
}

// TestWriteScrubbedWritesOnSuccess is the control: when scrubbing succeeds
// the file is written normally.
func TestWriteScrubbedWritesOnSuccess(t *testing.T) {
	dir := t.TempDir()

	in := `[{"name":"cluster_type","value":"k8s"}]`
	require.NoError(t, writeScrubbed(dir, "plan_variables.json", []byte(in), bamboo.Scrubber{}))
	out, err := os.ReadFile(filepath.Join(dir, "plan_variables.json"))
	require.NoError(t, err)
	assert.Contains(t, string(out), "value-1")
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func testRecorder(t *testing.T, routes map[string]string) *recorder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &recorder{base: srv.URL, token: "test-token", http: srv.Client(), out: t.TempDir()}
}

func TestPlaceholderNamesCoversEveryProjectAndRepository(t *testing.T) {
	r := testRecorder(t, map[string]string{
		"/rest/api/latest/project/OPS": `{"key":"OPS","name":"ops-lab"}`,
		"/rest/api/latest/project": `{"projects":{"project":[{"key":"OPS","name":"ops-lab"},` +
			`{"key":"WEB","name":"web-shop"},{"key":"DATA","name":"DATA"}]}}`,
		"/rest/api/latest/result/OPS-PROV-3": `{"vcsRevisions":{"vcsRevision":[{"repositoryName":"ops-infra"},{"repositoryName":"ops-charts"}]}}`,
	})

	names, err := r.placeholderNames("OPS", "OPS-PROV-3", "LAB")
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"OPS":        "LAB",
		"ops-lab":    "lab",
		"WEB":        "LAB2",
		"web-shop":   "lab2",
		"DATA":       "LAB3",
		"ops-infra":  "lab-repo-1",
		"ops-charts": "lab-repo-2",
	}, names, "every project on the server is renamed, not only the recorded one")
}

func TestWarnResidualReportsAnotherUser(t *testing.T) {
	r := testRecorder(t, nil)
	r.scrub = bamboo.Scrubber{Names: map[string]string{"IT": "LAB"}}
	require.NoError(t, os.WriteFile(filepath.Join(r.out, "results.json"),
		[]byte(`{"a":"/browse/user/jdoe","b":"/browse/user/rsmith"}`), 0o644))

	out := captureStdout(t, func() { require.NoError(t, r.warnResidual()) })

	assert.Contains(t, out, `user name "rsmith" is still in the recording`)
	assert.NotContains(t, out, "jdoe")
	assert.Contains(t, out, `"IT" is short enough`)
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	rd, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	f()
	require.NoError(t, w.Close())
	os.Stdout = old
	data, err := io.ReadAll(rd)
	require.NoError(t, err)
	return string(data)
}

// A name right after an HTML tag, as in a build reason's link text, must be
// scrubbed too. Pretty-printing used to escape ">" to >, whose last
// letter hid the name from the scrubber's word boundary.
func TestWriteScrubbedScrubsNamesInsideHTML(t *testing.T) {
	dir := t.TempDir()
	in := `{"buildReason":"Child of <a href=\"http://bamboo.lab.example/browse/WORK-PARENT-2\">WORK-PARENT-2</a>"}`
	for _, file := range []string{"result.json", "plan_variables.json"} {
		require.NoError(t, writeScrubbed(dir, file, []byte(in), bamboo.Scrubber{Host: "bamboo.lab.example", Names: map[string]string{"WORK": "LAB"}}))
		out, err := os.ReadFile(filepath.Join(dir, file))
		require.NoError(t, err)
		assert.NotContains(t, string(out), "WORK", file)
		assert.Contains(t, string(out), ">LAB-PARENT-2</a>", file)
	}
}

func TestMoveLegacyConfigKeepsDenylistTerms(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "old", "bam")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "new"))
	t.Setenv("BAM_FIXTURE_DENYLIST", "")
	require.NoError(t, os.MkdirAll(old, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(old, "fixture-denylist.txt"), []byte("earlier-term\n"), 0o600))
	cfgDir := filepath.Join(base, "new", "bam")
	opts := toolcfg.Options{
		MachineFile: filepath.Join(cfgDir, "config.yaml"),
		Credentials: filepath.Join(cfgDir, "credentials.yaml"),
		Getenv:      os.Getenv,
	}

	var out bytes.Buffer
	require.NoError(t, moveLegacyConfig(&out, opts, old))
	assert.Equal(t, "moved fixture-denylist.txt from "+old+" to "+cfgDir+"\n", out.String())

	r := &recorder{scrub: bamboo.Scrubber{Host: "ci.home.internal:8085"}}
	require.NoError(t, r.updateDenylist())
	terms, err := bamboo.LoadDenylist(filepath.Join(cfgDir, "fixture-denylist.txt"))
	require.NoError(t, err)
	assert.Contains(t, terms, "earlier-term")
	assert.Contains(t, terms, "ci.home.internal")
	assert.NoFileExists(t, filepath.Join(old, "fixture-denylist.txt"))
}

func TestMoveLegacyConfigLeavesFilesAnOverrideNames(t *testing.T) {
	base := t.TempDir()
	old, cfgDir := filepath.Join(base, "old"), filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(old, 0o700))
	machine, denylist := filepath.Join(old, "config.yaml"), filepath.Join(old, "fixture-denylist.txt")
	require.NoError(t, os.WriteFile(machine, []byte("version: 1\n"), 0o600))
	require.NoError(t, os.WriteFile(denylist, []byte("term\n"), 0o600))
	opts := toolcfg.Options{
		MachineFile: machine,
		Credentials: filepath.Join(cfgDir, "credentials.yaml"),
		Getenv: func(k string) string {
			if k == "BAM_FIXTURE_DENYLIST" {
				return denylist
			}
			return ""
		},
	}

	var out bytes.Buffer
	require.NoError(t, moveLegacyConfig(&out, opts, old))
	assert.Empty(t, out.String())
	assert.FileExists(t, machine)
	assert.FileExists(t, denylist)
}
