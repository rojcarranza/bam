package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func okProbe() []bamboo.ProbeResult {
	return []bamboo.ProbeResult{
		{Name: "server version", Status: bamboo.ProbeOK, Detail: "9.6.2"},
		{Name: "plan variables", Status: bamboo.ProbeOK, Detail: "0 declared on PROJ-BUILD"},
		{Name: "build variables", Status: bamboo.ProbeUnsupported, Detail: "--from works only for builds bam triggered"},
		{Name: "logs", Status: bamboo.ProbeOK, Detail: "via entries"},
		{Name: "failed tests", Status: bamboo.ProbeSkipped, Detail: "no failed build of PROJ-BUILD to read"},
		{Name: "stop builds", Status: bamboo.ProbeSkipped, Detail: "learned the first time bam build cancel runs"},
	}
}

func TestDoctorHappyPath(t *testing.T) {
	h := newHarness(t)
	h.probe = okProbe()
	assert.Equal(t, 0, h.run("doctor"))
	out := h.stdout.String()
	assert.Regexp(t, `✓ config\s+.*\.bam\.yaml`, out)
	assert.Regexp(t, `✓ server\s+work \(https://bamboo.example.com\)`, out)
	assert.Regexp(t, `✓ token\s+keychain`, out)
	assert.Regexp(t, `✓ auth\s+jdoe \(J Doe\)`, out)
	assert.Regexp(t, `! build variables\s+not supported: --from works only for builds bam triggered`, out)
	assert.Regexp(t, `– stop builds\s+learned the first time`, out)
}

func TestDoctorStopsAtMissingToken(t *testing.T) {
	h := newHarness(t)
	h.kr.m = map[string]string{}
	assert.Equal(t, 4, h.run("doctor"))
	assert.Regexp(t, `✗ token\s+no token for server "work"`, h.stdout.String())
	assert.Contains(t, h.stdout.String(), "bam login work")
	assert.NotContains(t, h.stdout.String(), "auth")
	assert.Empty(t, h.stderr.String())
}

func TestDoctorRejectedToken(t *testing.T) {
	h := newHarness(t)
	h.fake.UserErr = errs.Authf("401 from bamboo.example.com").WithTry("bam doctor")
	assert.Equal(t, 4, h.run("doctor"))
	assert.Regexp(t, `✗ auth\s+401 from bamboo.example.com`, h.stdout.String())
}

func TestDoctorBadConfig(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte("version: 2\n"), 0o644))
	assert.Equal(t, 3, h.run("doctor"))
	assert.Regexp(t, `✗ config\s+.*version 2`, h.stdout.String())
}

func TestDoctorProbeErrorExitsFiveButReportsAll(t *testing.T) {
	h := newHarness(t)
	h.probe = okProbe()
	h.probe[3] = bamboo.ProbeResult{Name: "logs", Status: bamboo.ProbeError, Detail: "Bamboo server error 500", Err: errs.Bamboof("Bamboo server error 500")}
	assert.Equal(t, 5, h.run("doctor"))
	assert.Regexp(t, `✗ logs\s+Bamboo server error 500`, h.stdout.String())
	assert.Contains(t, h.stdout.String(), "stop builds", "checks after the failure still run")
}

func TestDoctorFailsWhenListingPlansFails(t *testing.T) {
	h := newHarness(t)
	h.probe = okProbe()
	h.fake.Plans = nil // the configured project cannot be listed
	assert.Equal(t, 5, h.run("doctor"), "a failed listing is a failed diagnosis, not a skipped check")
	assert.Regexp(t, `✗ capabilities\s+project PROJ not found`, h.stdout.String())
}

func TestDoctorJSON(t *testing.T) {
	h := newHarness(t)
	h.probe = okProbe()
	assert.Equal(t, 0, h.run("doctor", "--json"))
	assert.Contains(t, h.stdout.String(), `"name": "auth"`)
	assert.Contains(t, h.stdout.String(), `"status": "unsupported"`)
}

func TestDoctorInvalidServerURL(t *testing.T) {
	h := newHarness(t)
	h.vars["BAM_URL"] = "not-a-url"
	assert.Equal(t, 3, h.run("doctor"))
	out := h.stdout.String()
	// Exactly one line should contain " server"
	serverLines := strings.Count(out, "server  ")
	assert.Equal(t, 1, serverLines, "expected exactly one 'server' check in output")
	// That line should start with ✗ server
	assert.Regexp(t, `✗ server`, out)
	// Should not contain ✓ server
	assert.NotContains(t, out, "✓ server")
}

func TestDoctorListsOnlyConfigFilesThatExist(t *testing.T) {
	h := newHarness(t)
	h.probe = okProbe()
	assert.Equal(t, 0, h.run("doctor"))
	assert.NotContains(t, h.stdout.String(), "config.yaml", "the machine file does not exist")

	require.NoError(t, os.Remove(filepath.Join(h.root, ".bam.yaml")))
	h.vars["BAM_URL"] = workOrigin
	h.vars["BAM_TOKEN"] = "tok"
	h.stdout.Reset()
	assert.Equal(t, 0, h.run("doctor"))
	assert.Regexp(t, `✓ config\s+no config file; settings come from the environment`, h.stdout.String())
}

func TestDoctorChecksThePlanKeyBeforeConnecting(t *testing.T) {
	for flag, try := range map[string]string{
		"PROJ-BUILD-482": "try: its plan, PROJ-BUILD",
		"PROJ":           "try: bam plan list PROJ to see its plans",
		"proj-build":     "try: keys are uppercase: did you mean PROJ-BUILD?",
	} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run("doctor", "--plan", flag), flag)
		assert.Empty(t, h.stdout.String(), flag)
		assert.Contains(t, h.stderr.String(), `"`+flag+`" is not a plan key`, flag)
		assert.Contains(t, h.stderr.String(), try, flag)
	}
}
