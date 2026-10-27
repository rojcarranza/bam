package bamboo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bamboo12Log serves a job's logEntries the way Bamboo 12.1.8 does: the
// logEntries[start:end] range and start-index are ignored, and max-result
// (default 25) picks the LAST entries. download answers the raw log, empty
// while the job runs.
type bamboo12Log struct {
	mu       sync.Mutex
	lines    []string
	download string
	firstN   bool // answer max-result with the FIRST entries instead, as a server might
}

func (b *bamboo12Log) grow(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i < n; i++ {
		b.lines = append(b.lines, fmt.Sprintf("line %d", len(b.lines)+1))
	}
}

func (b *bamboo12Log) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/download/") {
			_, _ = w.Write([]byte(b.download))
			return
		}
		n := 25
		for _, k := range []string{"max-result", "max-results"} {
			if v, err := strconv.Atoi(r.URL.Query().Get(k)); err == nil {
				n = v
			}
		}
		n = min(n, len(b.lines))
		type entry struct {
			Log      string `json:"log"`
			Unstyled string `json:"unstyledLog"`
		}
		var es []entry
		page := b.lines[len(b.lines)-n:]
		if b.firstN {
			page = b.lines[:n]
		}
		for _, l := range page {
			es = append(es, entry{l, l})
		}
		body, _ := json.Marshal(map[string]any{"key": "LAB-VARS-RUN-1", "logEntries": map[string]any{
			"size": len(b.lines), "start-index": 0, "max-result": n, "logEntry": es}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()})
	require.NoError(t, err)
	knownCaps(c, Capabilities{})
	return c
}

func TestFetchLogReadsTheWholeLogFromAServerThatReturnsTheLastEntries(t *testing.T) {
	b := &bamboo12Log{}
	b.grow(66)
	c := b.client(t)
	chunk, err := c.FetchLog(ctx, "LAB-VARS-RUN-1", provider.LogOptions{})
	require.NoError(t, err)
	require.Len(t, chunk.Lines, 66)
	assert.Equal(t, "line 1", chunk.Lines[0], "the log starts at its first line, not 25 from the end")
	assert.Equal(t, 66, chunk.Next)
}

func TestFetchLogFromAnOffsetReturnsTheLinesAfterIt(t *testing.T) {
	b := &bamboo12Log{}
	b.grow(66)
	chunk, err := b.client(t).FetchLog(ctx, "LAB-VARS-RUN-1", provider.LogOptions{Offset: 20})
	require.NoError(t, err)
	require.Len(t, chunk.Lines, 46)
	assert.Equal(t, "line 21", chunk.Lines[0])
	assert.Equal(t, 66, chunk.Next)
}

func TestFollowingAGrowingLogNeitherRepeatsNorSkips(t *testing.T) {
	b := &bamboo12Log{}
	b.grow(30)
	c := b.client(t)
	var got []string
	next := 0
	for _, more := range []int{0, 4, 0, 40} {
		b.grow(more)
		chunk, err := c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{Offset: next})
		require.NoError(t, err)
		got, next = append(got, chunk.Lines...), chunk.Next
	}
	require.Len(t, got, 74)
	for i, l := range got {
		require.Equal(t, fmt.Sprintf("line %d", i+1), l)
	}
}

func TestFetchLogPastTheReadLimitKeepsLineNumbersRight(t *testing.T) {
	// Bamboo 12 answers with the LAST logMax entries: they are lines 3 to 12
	// of 12, and following on from there neither repeats nor stalls.
	defer func(n int) { logMax = n }(logMax)
	logMax = 10
	b := &bamboo12Log{}
	b.grow(12)
	c := b.client(t)
	chunk, err := c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{})
	require.NoError(t, err)
	assert.Equal(t, "line 3", chunk.Lines[0])
	assert.Equal(t, "line 12", chunk.Lines[len(chunk.Lines)-1])
	assert.Equal(t, 12, chunk.Next)

	b.grow(3)
	chunk, err = c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{Offset: chunk.Next})
	require.NoError(t, err)
	assert.Equal(t, []string{"line 13", "line 14", "line 15"}, chunk.Lines)
	assert.Equal(t, 15, chunk.Next)

	chunk, err = c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{Offset: 8})
	require.NoError(t, err)
	assert.Equal(t, "line 9", chunk.Lines[0], "an offset inside the page starts there")
}

func TestFetchLogPastTheReadLimitOfAServerThatCountsFromTheStart(t *testing.T) {
	defer func(n int) { logMax = n }(logMax)
	logMax = 10
	b := &bamboo12Log{firstN: true}
	b.grow(12)
	chunk, err := b.client(t).FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{})
	require.NoError(t, err)
	assert.Equal(t, "line 1", chunk.Lines[0])
	assert.Len(t, chunk.Lines, 10)
	assert.Equal(t, 10, chunk.Next)
}

func TestFetchLogPastTheReadLimitNeverMixesInTheDownload(t *testing.T) {
	// The download counts agent lines the entries leave out, so an offset
	// from one is wrong in the other.
	defer func(n int) { logMax = n }(logMax)
	logMax = 10
	b := &bamboo12Log{download: "simple\t11-Oct-2026 16:26:37\tagent ready\nbuild\t11-Oct-2026 16:26:38\tline 1\n"}
	b.grow(12)
	chunk, err := b.client(t).FetchLog(ctx, "LAB-VARS-RUN-1", provider.LogOptions{})
	require.NoError(t, err)
	assert.NotContains(t, chunk.Lines, "agent ready")
	assert.Equal(t, 12, chunk.Next)
}

func TestFetchLogDownloadKeepsTheLastReadLimitLines(t *testing.T) {
	// Like the entries, the download path hands back at most logMax lines,
	// the last ones, with Next still counting the whole log.
	defer func(n int) { logMax = n }(logMax)
	logMax = 3
	var raw strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&raw, "build\t11-Oct-2026 16:26:38\tline %d\n", i)
	}
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/LAB-SLOW-ONE-5":               {status: 400, body: `{"message":"bad expand"}`},
		"GET /download/LAB-SLOW-ONE/build_logs/LAB-SLOW-ONE-5.log": {body: raw.String()},
	})
	knownCaps(c, Capabilities{})
	chunk, err := c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"line 3", "line 4", "line 5"}, chunk.Lines)
	assert.Equal(t, 5, chunk.Next)

	chunk, err = c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{Offset: 4})
	require.NoError(t, err)
	assert.Equal(t, []string{"line 5"}, chunk.Lines, "an offset inside the kept lines starts there")
}
