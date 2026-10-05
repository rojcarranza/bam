package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleWADL = `<?xml version="1.0" encoding="UTF-8"?>
<application xmlns="http://wadl.dev.java.net/2009/02">
  <resources base="http://bamboo.example.com/rest/api/latest/">
    <resource path="queue">
      <method name="GET" id="getQueue"/>
      <resource path="{projectKey}-{buildKey}">
        <param name="projectKey" style="template"/>
        <method name="POST" id="queueBuild">
          <request>
            <param name="executeAllStages" style="query"/>
            <param name="customRevision" style="query"/>
            <param name="stage" style="query"/>
          </request>
        </method>
      </resource>
    </resource>
    <resource path="result">
      <method name="GET" id="getResults">
        <request><param name="verboseLogs" style="query"/></request>
      </method>
    </resource>
  </resources>
</application>`

func TestSummarizeWADLListsQueueMethodsAndParams(t *testing.T) {
	s, err := summarizeWADL([]byte(sampleWADL))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"GET queue",
		"POST queue/{projectKey}-{buildKey}: executeAllStages customRevision stage",
	}, s.Queue)
}

func TestSummarizeWADLFindsVerboseParamsAnywhere(t *testing.T) {
	s, err := summarizeWADL([]byte(sampleWADL))
	require.NoError(t, err)
	assert.Equal(t, []string{"GET result: verboseLogs"}, s.Verbose)
}

func TestSummarizeWADLRejectsNonXML(t *testing.T) {
	_, err := summarizeWADL([]byte(`{"not":"xml"}`))
	assert.Error(t, err)
}

func TestOlderRevisionSkipsTheNewest(t *testing.T) {
	assert.Equal(t, "bbb", olderRevision([]string{"aaa", "aaa", "bbb", "ccc"}))
	assert.Equal(t, "", olderRevision([]string{"aaa", "aaa"}))
	assert.Equal(t, "", olderRevision(nil))
}

func TestVerboseQueueParamIgnoresTheDeploymentQueue(t *testing.T) {
	s := wadlSummary{Verbose: []string{"POST queue/deployment: verboseLogging"}}
	assert.Equal(t, "", verboseQueueParam(s), "Bamboo 12.1.8 has verboseLogging on deployments only")

	s.Verbose = append(s.Verbose, "POST queue/{projectKey : ([^-/]+)}-{buildKey}: verboseLogs")
	assert.Equal(t, "verboseLogs", verboseQueueParam(s))
}

// A refused restart is recorded as .status and reported as not accepted, so
// probeRerun does not wait minutes for a restart that never happened.
func TestRestartReportsARefusal(t *testing.T) {
	r := testRecorder(t, map[string]string{})
	accepted, err := r.restart("PROJ-BUILD-45", "queue_restart.json")
	require.NoError(t, err)
	require.False(t, accepted)
	_, statErr := os.Stat(filepath.Join(r.out, "queue_restart.json.status"))
	require.NoError(t, statErr)

	r = testRecorder(t, map[string]string{"/rest/api/latest/queue/PROJ-BUILD-45": `{"buildResultKey":"PROJ-BUILD-45"}`})
	accepted, err = r.restart("PROJ-BUILD-45", "queue_restart.json")
	require.NoError(t, err)
	require.True(t, accepted)
}
