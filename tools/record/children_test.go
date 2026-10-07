package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dependencyWADL = `<?xml version="1.0" encoding="UTF-8"?>
<application xmlns="http://wadl.dev.java.net/2009/02">
  <resources base="http://bamboo.example.com/rest/api/latest/">
    <resource path="plan">
      <resource path="{projectKey}-{buildKey}">
        <method name="GET" id="getPlan"/>
        <resource path="dependencies">
          <method name="GET" id="getDependencies"/>
        </resource>
      </resource>
    </resource>
    <resource path="result">
      <method name="GET" id="getResults"/>
    </resource>
  </resources>
</application>`

func TestSummarizeWADLListsDependencyPaths(t *testing.T) {
	s, err := summarizeWADL([]byte(dependencyWADL))
	require.NoError(t, err)
	require.Equal(t, []string{"GET plan/{projectKey}-{buildKey}/dependencies"}, s.Related)
}

func TestMentionsBuild(t *testing.T) {
	assert.True(t, mentionsBuild(`Child of <a href="/browse/LAB-SPECS-7">LAB-SPECS-7</a>`, "LAB-SPECS-7"))
	assert.True(t, mentionsBuild(`Child of LAB-SPECS-7`, "LAB-SPECS-7"))
	assert.False(t, mentionsBuild(`Child of LAB-SPECS-70`, "LAB-SPECS-7"), "another build number")
	assert.False(t, mentionsBuild(`Child of XLAB-SPECS-7`, "LAB-SPECS-7"), "another plan")
	assert.False(t, mentionsBuild("Manual run by Admin", "LAB-SPECS-7"))
}

func TestChildResults(t *testing.T) {
	body := `{"results":{"result":[
		{"buildResultKey":"LAB-PROV-20","buildReason":"Child of <a href=\"/browse/LAB-SPECS-7\">LAB-SPECS-7</a>"},
		{"buildResultKey":"LAB-DEPROV-3","buildReason":"Manual run by Admin"},
		{"buildResultKey":"LAB-SPECS-7","buildReason":"Manual run by Admin"}]}}`
	keys, err := childResults([]byte(body), "LAB-SPECS-7")
	require.NoError(t, err)
	require.Equal(t, []string{"LAB-PROV-20"}, keys)
	_, err = childResults([]byte("not json"), "LAB-SPECS-7")
	require.Error(t, err)
}
