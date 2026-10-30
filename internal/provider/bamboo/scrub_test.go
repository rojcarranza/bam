package bamboo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScrubReplacesHostsUsersAndEmails(t *testing.T) {
	in := `{"link":{"href":"http://ci.home.internal:8085/rest/api/latest/plan/LAB-PROV"},` +
		`"repo":"https://git.home.internal/x.git","reason":"Manual run by <a href=\"http://ci.home.internal:8085/browse/user/rsmith\">Real Name</a>",` +
		`"email":"real.person@home.internal","raw":"agent on ci.home.internal:8085","name":"rsmith"}`
	s := Scrubber{Host: "ci.home.internal:8085", Users: []string{"Real Name", "rsmith"}}
	out := string(s.Scrub([]byte(in)))

	assert.NotContains(t, out, "home.internal")
	assert.NotContains(t, out, "Real Name")
	assert.NotContains(t, out, `"rsmith"`)
	assert.Contains(t, out, "http://bamboo.example.com/rest/api/latest/plan/LAB-PROV")
	assert.Contains(t, out, "jdoe@example.com")
	assert.Contains(t, out, `"name":"jdoe"`)
}

func TestFixtureHostViolations(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.json"), []byte(`{"u":"https://bamboo.example.com/x","v":"http://localhost:8085"}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "recorded"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "recorded", "bad.json"), []byte(`{"u":"https://bamboo.corp.internal/x"}`), 0o644))

	v, err := FixtureHostViolations(dir)
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Contains(t, v[0], "bad.json")
	assert.Contains(t, v[0], "bamboo.corp.internal")
}

func TestScrubBareHostsAndDifferentCase(t *testing.T) {
	// Test bare hostname without protocol
	in := `{"agent":"ci.home.internal","name":"test"}`
	s := Scrubber{Host: "ci.home.internal", Users: nil}
	out := string(s.Scrub([]byte(in)))
	assert.NotContains(t, out, "ci.home.internal")
	assert.Contains(t, out, "jdoe")

	// Test different case
	in = `{"host":"CI.HOME.INTERNAL","link":"http://CI.HOME.INTERNAL:8085/x"}`
	s = Scrubber{Host: "ci.home.internal:8085", Users: nil}
	out = string(s.Scrub([]byte(in)))
	assert.NotContains(t, out, "CI.HOME.INTERNAL")
	assert.NotContains(t, out, "ci.home.internal")
}

func TestScrubberTerms(t *testing.T) {
	s := Scrubber{Host: "ci.home.internal:8085", Users: []string{"Real Name", "rsmith", "", "Real Name"}}
	terms := s.Terms()
	assert.Contains(t, terms, "ci.home.internal:8085")
	assert.Contains(t, terms, "ci.home.internal")
	assert.Contains(t, terms, "Real Name")
	assert.Contains(t, terms, "rsmith")
	assert.NotContains(t, terms, "")
	// Check no duplicates: convert to map and verify size matches
	termMap := make(map[string]bool)
	for _, term := range terms {
		termMap[term] = true
	}
	assert.Equal(t, len(terms), len(termMap), "terms should have no duplicates, got: %v", terms)
}

func TestFixtureHostViolationsAnyScheme(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.json"), []byte(`{"u":"https://bamboo.example.com/x"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ws.json"), []byte(`{"u":"wss://ci.corp.internal/socket"}`), 0o644))

	v, err := FixtureHostViolations(dir)
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Contains(t, v[0], "ws.json")
	assert.Contains(t, v[0], "ci.corp.internal")
}

func TestFixtureHostViolationsIPv6Bracket(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.json"), []byte(`{"u":"http://[::1]:8085/x"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"u":"http://[2001:db8::1]:8085/x"}`), 0o644))

	v, err := FixtureHostViolations(dir)
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Contains(t, v[0], "bad.json")
	assert.Contains(t, v[0], "[2001:db8::1]")
}

func TestFixtureHostViolationsIPv6BracketIgnoresNonIPCandidates(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.json"), []byte(
		`{"expand":"logEntries[0:50]","idx":"[1]","letters":"[abc]"}`), 0o644))

	v, err := FixtureHostViolations(dir)
	require.NoError(t, err)
	assert.Empty(t, v, "expand ranges and other bracketed non-IPv6 text must not be flagged: %v", v)
}

func TestFixtureHostViolationsIPAddress(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "has_ip.json"), []byte(`{"host":"10.1.2.3"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "has_localhost.json"), []byte(`{"host":"127.0.0.1"}`), 0o644))

	v, err := FixtureHostViolations(dir)
	require.NoError(t, err)
	// Should find 10.1.2.3 but not 127.0.0.1
	require.Len(t, v, 1)
	assert.Contains(t, v[0], "10.1.2.3")
	assert.NotContains(t, strings.Join(v, ","), "127.0.0.1")
}

func TestFixtureTermViolations(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), []byte(`{"user":"RSMITH","data":"some data"}`), 0o644))

	v, err := FixtureTermViolations(dir, []string{"rsmith"})
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Contains(t, v[0], "test.json")
	assert.Contains(t, v[0], "rsmith")
}

func TestLoadDenylist(t *testing.T) {
	dir := t.TempDir()
	denylistPath := filepath.Join(dir, "denylist.txt")

	// Test missing file returns nil
	v, err := LoadDenylist(filepath.Join(dir, "missing.txt"))
	require.NoError(t, err)
	require.Nil(t, v)

	// Test loading with comments and blanks
	content := `# Comment
jdoe
ci.home.internal

# Another comment
rsmith`
	require.NoError(t, os.WriteFile(denylistPath, []byte(content), 0o644))

	v, err = LoadDenylist(denylistPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, v, []string{"jdoe", "ci.home.internal", "rsmith"})
}

func TestDenylistPath(t *testing.T) {
	// Test with BAM_FIXTURE_DENYLIST set
	path := DenylistPath(func(key string) string {
		if key == "BAM_FIXTURE_DENYLIST" {
			return "/custom/path"
		}
		return ""
	})
	assert.Equal(t, "/custom/path", path)

	// Test default path
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	path = DenylistPath(func(string) string { return "" })
	assert.Equal(t, filepath.Join(home, ".config", "bam", "fixture-denylist.txt"), path)

	path = DenylistPath(func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return "/xdg"
		}
		return ""
	})
	assert.Equal(t, filepath.Join("/xdg", "bam", "fixture-denylist.txt"), path)
}

func TestFixtureTermViolationsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), []byte(`{"host":"CI.HOME.INTERNAL","name":"RSMITH"}`), 0o644))

	v, err := FixtureTermViolations(dir, []string{"ci.home.internal", "rsmith"})
	require.NoError(t, err)
	assert.Equal(t, 2, len(v))
}

func TestScrubReplacesNamesWithPlaceholders(t *testing.T) {
	s := Scrubber{
		Host:  "bamboo.lab.example:8085",
		Users: []string{"rsmith"},
		Names: map[string]string{"proj-ci": "lab-ci", "PROJ": "LAB"},
	}

	out := string(s.Scrub([]byte(`{"key":"PROJ-PROV-2","planName":"proj-ci build","repositoryName":"proj-ci","projectName":"proj"}`)))

	assert.Contains(t, out, `"key":"LAB-PROV-2"`)
	assert.Contains(t, out, `"planName":"lab-ci build"`, "the longer name is replaced before the shorter one")
	assert.Contains(t, out, `"repositoryName":"lab-ci"`)
	assert.Contains(t, out, `"projectName":"LAB"`, "a differently cased mention is replaced too")
	assert.Contains(t, out, `"projectName"`, "the field name itself is left alone")

}

func TestDenylistTermsIncludeReplacedNames(t *testing.T) {
	s := Scrubber{Host: "bamboo.lab.example:8085", Users: []string{"rsmith"}, Names: map[string]string{"proj-ci": "lab-ci"}}

	terms := s.DenylistTerms()

	assert.Contains(t, terms, "proj-ci")
	assert.Contains(t, terms, "bamboo.lab.example:8085")
	assert.Contains(t, terms, "rsmith")
	assert.NotContains(t, s.Terms(), "proj-ci", "Scrub must not rewrite a project name to jdoe")
}

// The host must be redacted before project names are, or a name that is a
// substring of the host mangles a bare host mention past both guards.
func TestScrubRedactsHostBeforeNames(t *testing.T) {
	s := Scrubber{Host: "bamboo.lab.example:8085", Names: map[string]string{"lab": "proj"}}

	out := string(s.Scrub([]byte(`{"agent":"runner on bamboo.lab.example:8085","project":"lab"}`)))

	assert.NotContains(t, out, "bamboo.lab.example")
	assert.Contains(t, out, `"project":"proj"`)
}

func TestScrubTreatsPlaceholderLiterally(t *testing.T) {
	s := Scrubber{Names: map[string]string{"OLD": "N$1EW"}}

	out := string(s.Scrub([]byte(`{"key":"OLD-1"}`)))

	assert.Equal(t, `{"key":"N$1EW-1"}`, out, "a $ in the placeholder is not a capture-group reference")
}

func TestShortNamesAreReported(t *testing.T) {
	s := Scrubber{Names: map[string]string{"IT": "LAB", "proj-ci": "lab-ci"}}

	assert.Equal(t, []string{"IT"}, s.RiskyNames(), "a short name matches unrelated words")
}

// The scrubber replaces project names as whole words, so the guard must not
// flag a term that only appears inside a longer identifier, such as a
// project key AGENT inside Bamboo's own field hasExecutableAgents.
func TestFixtureTermViolationsIgnoresTermsInsideLongerWords(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inside.json"), []byte(`{"hasExecutableAgents":true}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "word.json"), []byte(`{"key":"AGENT-BUILD-3"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "under.json"), []byte(`{"dir":"agent_home"}`), 0o644))

	v, err := FixtureTermViolations(dir, []string{"AGENT"})

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(dir, "word.json") + ": AGENT",
		filepath.Join(dir, "under.json") + ": AGENT",
	}, v)
}

// A commit author is not the recording user, so Users does not list them.
// Bamboo shows them in a build reason as link text and, percent-encoded, in
// the author link.
func TestScrubCommitAuthors(t *testing.T) {
	in := `{"buildReason":"Changes by <a href=\"http://ci.home.internal/authors/viewAuthor.action?authorName=Jane%20Roe%20%3Cjane.roe%40corp.home%3E\">Jane Roe &lt;jane.roe@corp.home&gt;</a>",` +
		`"author":"Jane Roe <jane.roe@corp.home>","plain":"see x%2Bnote%40corp.home"}`
	out := string(Scrubber{Host: "ci.home.internal"}.Scrub([]byte(in)))
	assert.NotContains(t, out, "Jane")
	assert.NotContains(t, out, "Roe")
	assert.NotContains(t, out, "corp.home")
	assert.Contains(t, out, "authorName=jdoe\\\"")
	assert.Contains(t, out, `>jdoe &lt;jdoe@example.com&gt;</a>`)
	assert.Contains(t, out, `"author":"jdoe <jdoe@example.com>"`)
	assert.Contains(t, out, "see jdoe%40example.com")
}

func TestFixtureHostViolationsEmails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.json"),
		[]byte(`{"a":"jdoe@example.com","b":"jdoe%40example.com","c":"real@corp.home","d":"authorName=x%3Creal%40corp.home%3E"}`), 0o644))
	v, err := FixtureHostViolations(dir)
	require.NoError(t, err)
	require.Len(t, v, 2)
	assert.Contains(t, v[0], "real@corp.home")
	assert.Contains(t, v[1], "real%40corp.home")
}
