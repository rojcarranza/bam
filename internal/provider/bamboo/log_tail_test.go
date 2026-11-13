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

	"github.com/rojcarranza/bam/internal/provider"
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
	firstN   bool  // answer max-result with the FIRST entries instead, as a server might
	served   []int // how many entries each read was sent
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
		b.served = append(b.served, len(es))
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

// sent is how many entries each read since the last call was sent.
func (b *bamboo12Log) sent() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.served
	b.served = nil
	return out
}

// lineRange is "line from" to "line to", empty when to is before from.
func lineRange(from, to int) []string {
	out := []string{}
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("line %d", i))
	}
	return out
}

// TestFollowingALongLogReadsOnlyWhatItGained: a watched tree reads a
// running job's log on every refresh. After the first read, a read from an
// offset is sent the log's newest entries, not all of it, even when the log
// gained more than one small read holds (#199).
func TestFollowingALongLogReadsOnlyWhatItGained(t *testing.T) {
	b := &bamboo12Log{}
	b.grow(3000)
	c := b.client(t)
	chunk, err := c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{})
	require.NoError(t, err)
	require.Equal(t, []int{3000}, b.sent(), "the first read is the whole log")
	next := chunk.Next
	for _, more := range []int{40, 0, logStep + 250, 1} {
		b.grow(more)
		chunk, err := c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{Offset: next})
		require.NoError(t, err)
		require.Equal(t, lineRange(next+1, next+more), chunk.Lines)
		require.Equal(t, next+more, chunk.Next)
		next = chunk.Next
		for _, n := range b.sent() {
			require.Less(t, n, 3000, "a follow is never sent the whole log")
		}
	}
}

// TestFollowingALongLogOnAServerThatCountsFromTheStart: a server that
// answers max-result with a log's first entries cannot be followed from
// its last ones, so its log is read whole, and still lines up.
func TestFollowingALongLogOnAServerThatCountsFromTheStart(t *testing.T) {
	b := &bamboo12Log{firstN: true}
	b.grow(3000)
	c := b.client(t)
	chunk, err := c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{})
	require.NoError(t, err)
	for _, more := range []int{10, 20} {
		b.grow(more)
		next := chunk.Next
		chunk, err = c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{Offset: next})
		require.NoError(t, err)
		require.Equal(t, lineRange(next+1, next+more), chunk.Lines)
	}
}

// TestFollowingALogWhoseEndsLookAlike: when the first and last entries of
// a read are the same lines, nothing tells which end of the log it is, so
// the log is read whole rather than taken for its last entries.
func TestFollowingALogWhoseEndsLookAlike(t *testing.T) {
	b := &bamboo12Log{firstN: true}
	b.grow(3000)
	for _, i := range []int{0, 1, 2, logStep - 3, logStep - 2, logStep - 1} {
		b.lines[i] = "Build log tailing is paused"
	}
	c := b.client(t)
	chunk, err := c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{})
	require.NoError(t, err)
	b.grow(10)
	chunk, err = c.FetchLog(ctx, "LAB-SLOW-ONE-5", provider.LogOptions{Offset: chunk.Next})
	require.NoError(t, err)
	require.Equal(t, lineRange(3001, 3010), chunk.Lines)
}
