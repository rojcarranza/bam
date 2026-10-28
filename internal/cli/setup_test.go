package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerAddListRm(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("server", "add", "home", "--url", "http://bamboo.lab.example:8085", "--project", "LAB"))
	assert.Contains(t, h.stdout.String(), "added server home")
	data, err := os.ReadFile(h.env.Paths.MachineConfig)
	require.NoError(t, err)
	assert.Contains(t, string(data), "url: http://bamboo.lab.example:8085")

	assert.Equal(t, 2, h.run("server", "add", "home", "--url", "http://bamboo.lab.example:8085"), "exists without --force")
	assert.Equal(t, 3, h.run("server", "add", "bad", "--url", "bamboo.lab.example"), "invalid URL is a config error")
	assert.Equal(t, 2, h.run("server", "add", "env", "--url", "https://x.example.com"), "env is reserved")

	assert.Equal(t, 0, h.run("server", "list"))
	assert.Contains(t, h.stdout.String(), "home")
	assert.Contains(t, h.stdout.String(), "work")
	assert.Regexp(t, `work\s+https://bamboo.example.com\s+project\s+yes\s+yes`, h.stdout.String())
	assert.Regexp(t, `home\s+http://bamboo.lab.example:8085\s+machine\s+no\s+no`, h.stdout.String())

	assert.Equal(t, 0, h.run("--server", "home", "server", "list", "--json"))
	assert.Contains(t, h.stdout.String(), `"alias": "home",
    "url": "http://bamboo.lab.example:8085",
    "defined_in": [
      "machine"
    ],
    "token": false,
    "selected": true`)

	assert.Equal(t, 0, h.run("server", "default"))
	assert.Empty(t, h.stdout.String())
	assert.Contains(t, h.stderr.String(), "no machine default server set")
	assert.Equal(t, 0, h.run("server", "default", "home"))
	assert.Equal(t, 0, h.run("server", "default"))
	assert.Equal(t, "home\n", h.stdout.String())
	assert.Equal(t, 0, h.run("server", "default", "--json"))
	assert.JSONEq(t, `{"default_server": "home"}`, h.stdout.String())
	data, _ = os.ReadFile(h.env.Paths.MachineConfig)
	assert.Contains(t, string(data), "default_server: home")
	assert.Equal(t, 2, h.run("server", "default", "work"), "a project alias cannot be the machine default")
	project := filepath.Join(h.root, ".bam.yaml")
	assert.Equal(t, "error: server \"work\" is defined in "+project+"\n"+
		"  the machine default must be a machine server, or every other repo would break\n"+
		"  try: set default_server: work in "+project+"\n", h.stderr.String())
	assert.Equal(t, 2, h.run("server", "default", "nope"))
	assert.Contains(t, h.stderr.String(), "no server alias \"nope\" in")

	assert.Equal(t, 0, h.run("server", "rm", "home"))
	assert.Contains(t, h.stdout.String(), "cleared the machine default_server")
	assert.NotContains(t, h.stdout.String(), "next:", "no machine server is left to pick")
	assert.Equal(t, 2, h.run("server", "rm", "work"))
	assert.Contains(t, h.stderr.String(), ".bam.yaml")
}

func TestLoginWithTokenFromStdin(t *testing.T) {
	h := newHarness(t)
	h.stdin = "newtok\n"
	assert.Equal(t, 0, h.run("login", "work", "--with-token"))
	assert.Contains(t, h.stdout.String(), "logged in to work as jdoe (J Doe)")
	assert.Equal(t, "newtok", h.connectOpts[len(h.connectOpts)-1].Token, "the new token is verified before storing")
	assert.Equal(t, "newtok", h.kr.m["bam|"+workOrigin])
}

func TestLoginRejectedStoresNothing(t *testing.T) {
	h := newHarness(t)
	h.fake.UserErr = errs.Authf("401 from bamboo.example.com")
	h.stdin = "bad\n"
	assert.Equal(t, 4, h.run("login", "work", "--with-token"))
	assert.Equal(t, "tok", h.kr.m["bam|"+workOrigin], "old token untouched")
}

func TestLoginPromptsOnlyOnTerminal(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("login", "work"))
	assert.Contains(t, h.stderr.String(), "--with-token")

	h.tty = true
	h.secret = "s3cret"
	assert.Equal(t, 0, h.run("login", "work"))
	assert.Contains(t, h.stderr.String(), "https://bamboo.example.com/profile/userAccessTokens.action")
	assert.Equal(t, "s3cret", h.kr.m["bam|"+workOrigin])
	assert.NotContains(t, h.stdout.String()+h.stderr.String(), "s3cret")
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("logout", "work"))
	assert.Contains(t, h.stdout.String(), "logged out of work")
	_, ok := h.kr.m["bam|"+workOrigin]
	assert.False(t, ok)
	assert.Equal(t, 0, h.run("logout", "work"))
	assert.Contains(t, h.stdout.String(), "no stored token for work")
}

func TestLoginAndLogoutTakeAliasFromServerFlag(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("logout", "--server", "work"))
	assert.Contains(t, h.stdout.String(), "logged out of work")
	_, ok := h.kr.m["bam|"+workOrigin]
	assert.False(t, ok)

	h.stdin = "s3cret\n"
	assert.Equal(t, 0, h.run("login", "--server", "work", "--with-token"))
	assert.Equal(t, "s3cret", h.kr.m["bam|"+workOrigin])

	assert.Equal(t, 2, h.run("logout"))
	assert.Contains(t, h.stderr.String(), "missing <alias>")
}

func TestWhoami(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("whoami"))
	assert.Regexp(t, `work\s+https://bamboo.example.com\s+jdoe \(J Doe\)`, h.stdout.String())

	assert.Equal(t, 0, h.run("server", "add", "home", "--url", "http://bamboo.lab.example:8085"))
	assert.Equal(t, 4, h.run("whoami"), "home has no token")
	assert.Contains(t, h.stdout.String(), "jdoe (J Doe)")
	assert.Contains(t, h.stdout.String(), "bam login home")
	assert.Empty(t, h.stderr.String(), "errors are already in the table")

	assert.Equal(t, 0, h.run("whoami", "--server", "work", "--json"))
	assert.JSONEq(t, `[{"alias":"work","url":"https://bamboo.example.com","user":"jdoe","full_name":"J Doe","error":""}]`, h.stdout.String())
}

func TestWhoamiWithNoServers(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.Remove(filepath.Join(h.root, ".bam.yaml")))
	assert.Equal(t, 0, h.run("whoami"))
	assert.Empty(t, h.stdout.String(), "no empty table")
	assert.Contains(t, h.stderr.String(), "no servers configured\n  try: bam server add <alias> --url URL")
	assert.Equal(t, 0, h.run("whoami", "--json"))
	assert.JSONEq(t, `[]`, h.stdout.String())
}

func TestLogoutUnknownServer(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 3, h.run("logout", "x"))
	assert.Equal(t, "error: no server alias \"x\"\n  servers: work\n", h.stderr.String(), "no hint to add a server just to log out of it")

	require.NoError(t, os.Remove(filepath.Join(h.root, ".bam.yaml")))
	assert.Equal(t, 3, h.run("logout", "x"))
	assert.Equal(t, "error: no server alias \"x\"\n  no servers are configured, so there is nothing to log out of\n", h.stderr.String())
}

func TestServerRmWithNoServers(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.Remove(filepath.Join(h.root, ".bam.yaml")))
	assert.Equal(t, 2, h.run("server", "rm", "x"))
	assert.Equal(t, "error: unknown server \"x\"\n  no servers are configured, so there is nothing to remove\n", h.stderr.String())
}

func TestServerDefaultWithNoServers(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.Remove(filepath.Join(h.root, ".bam.yaml")))
	assert.Equal(t, 2, h.run("server", "default", "x"))
	assert.Equal(t, "error: unknown server \"x\"\n  no servers are configured\n  try: bam server add x --url URL\n", h.stderr.String())
}

func TestProjectFlagMustBeAProjectKey(t *testing.T) {
	for _, tc := range []struct{ arg, try string }{
		{"proj", "did you mean PROJ?"},
		{"PROJ-PLAN", "PROJ-PLAN is a plan key; its project is PROJ"},
		{"PROJ,,", "project keys are uppercase letters and digits, like PROJ"},
	} {
		for _, args := range [][]string{
			{"init", "--server", "work", "--url", "https://bamboo.example.com", "--project", tc.arg, "--force"},
			{"server", "add", "home", "--url", "https://bamboo.example.com", "--project", tc.arg},
		} {
			h := newHarness(t)
			assert.Equal(t, 2, h.run(args...), args)
			assert.Contains(t, h.stderr.String(), "is not a project key", args)
			assert.Contains(t, h.stderr.String(), tc.try, args)
		}
	}
}

func TestServerCompletion(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("server", "add", "home", "--url", "http://bamboo.lab.example:8085"))

	h.run("__complete", "login", "")
	assert.Contains(t, h.stdout.String(), "home\nwork\n", "login offers every alias")
	h.run("__complete", "run", "build", "--server", "")
	assert.Contains(t, h.stdout.String(), "home\nwork\n", "--server ignores the positional args")
	h.run("__complete", "server", "rm", "")
	assert.Contains(t, h.stdout.String(), "home\n")
	assert.NotContains(t, h.stdout.String(), "work", "rm offers only machine aliases")
	h.run("__complete", "login", "home", "")
	assert.NotContains(t, h.stdout.String(), "work", "one alias only")
}
