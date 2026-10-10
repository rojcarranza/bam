package tui

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// TestOSC52WritesTheEscapeSequence. The payload is base64, which is what a
// terminal expects, and what makes this work through ssh and tmux.
func TestOSC52WritesTheEscapeSequence(t *testing.T) {
	var buf bytes.Buffer
	url := labOrigin + "/browse/PROJ-BUILD-44"
	require.NoError(t, osc52(&buf, url))
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(url)) + "\x07"
	require.Equal(t, want, buf.String())
}

func TestOSC52WithNoWriterIsNotAnError(t *testing.T) {
	require.NoError(t, osc52(nil, "anything"))
}

// TestSelectionURLFollowsFocus, so o and y always mean the thing on screen.
func TestSelectionURLFollowsFocus(t *testing.T) {
	m := goldenModel(80, 24)
	m.plans.setItems([]provider.Plan{{Key: "PROJ-BUILD", URL: labOrigin + "/browse/PROJ-BUILD"}})
	m.builds.setItems([]provider.Build{{Key: "PROJ-BUILD-44", URL: labOrigin + "/browse/PROJ-BUILD-44"}})

	m.focus = focusPlans
	require.Equal(t, labOrigin+"/browse/PROJ-BUILD", m.selectionURL())

	m.focus = focusBuilds
	require.Equal(t, labOrigin+"/browse/PROJ-BUILD-44", m.selectionURL())

	m.focus = focusStages
	m.detail = ptr(sampleBuild())
	m.expanded = map[string]bool{"Test": true}
	m.treeCursor = 2 // the Integration job
	require.Equal(t, sampleBuild().Stages[1].Jobs[0].URL, m.selectionURL())

	m.treeCursor = 0 // a stage has no URL of its own; the build's stands in
	require.Equal(t, sampleBuild().URL, m.selectionURL())
}

// TestSelectionURLOnTheLogScreenIsTheJob.
func TestSelectionURLOnTheLogScreenIsTheJob(t *testing.T) {
	m := logModel()
	require.Equal(t, labOrigin+"/browse/PROJ-BUILD-INT-44", m.selectionURL())
}

func TestOOpensTheSelection(t *testing.T) {
	var opened []string
	m := New(Deps{Open: func(u string) error { opened = append(opened, u); return nil }})
	m.width, m.height = 80, 24
	m.builds.setItems([]provider.Build{{Key: "PROJ-BUILD-44", URL: labOrigin + "/browse/PROJ-BUILD-44"}})
	m.focus = focusBuilds

	_, cmd := send(m, mkKey("o"))
	require.NotNil(t, cmd)
	cmd()
	require.Equal(t, []string{labOrigin + "/browse/PROJ-BUILD-44"}, opened)
}

func TestYCopiesAndSaysSo(t *testing.T) {
	var clip bytes.Buffer
	m := New(Deps{Clipboard: &clip})
	m.width, m.height = 80, 24
	m.builds.setItems([]provider.Build{{Key: "PROJ-BUILD-44", URL: labOrigin + "/browse/PROJ-BUILD-44"}})
	m.focus = focusBuilds

	m, cmd := send(m, mkKey("y"))
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.Contains(t, clip.String(), "\x1b]52;c;")
	require.Contains(t, m.status, "copied")
	require.Contains(t, m.View(), "copied")
}

func TestOAndYWithNothingSelectedDoNothing(t *testing.T) {
	m := New(Deps{})
	m.width, m.height = 80, 24
	for _, k := range []string{"o", "y"} {
		_, cmd := send(m, mkKey(k))
		require.Nil(t, cmd, "key %q", k)
	}
}
