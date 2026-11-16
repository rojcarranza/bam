package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/stretchr/testify/require"
)

// A set held by another bam is re-read every few seconds. Opening one of its
// builds ends the poll with the screen; esc back must start it again, or the
// set screen stays frozen until r (BAM-53, machine 9).
func TestWayBackFromASetBuildRestartsThePoll(t *testing.T) {
	m := setScreenModel(t, &app.SetRecord{ID: "release-1", Set: "release", Status: app.SetRunning,
		Stages: failedRec().Stages}, "pid 77 on desk, since 09:15")
	beat := m.setPollGen // the poll's beat in flight on the set screen
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)

	m, _ = send(m, mkKey("esc")) // Stages back to Builds
	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen)
	require.NotNil(t, cmd, "the set's poll starts again")

	_, load := send(m, setPollMsg{Gen: beat})
	require.Nil(t, load, "the beat of the loop that ended with the build is dropped")
	_, load = send(m, setPollMsg{Gen: m.setPollGen})
	require.NotNil(t, load, "the new loop's beat re-reads the set")
}

// A tree opened on a set held elsewhere goes back to that set when the
// server switches; the set's poll must run again there.
func TestServerSwitchFromATreeOnTheSetRestartsThePoll(t *testing.T) {
	m := setScreenModel(t, failedRec(), "pid 77 on desk, since 09:15")
	svc := m.svc
	beat := m.setPollGen // the poll's beat in flight on the set screen
	next, _ := m.openTree("PROJ-X-1")
	m = next.(Model)
	require.Equal(t, screenTree, m.screen)

	m, cmd := send(m, switchServerMsg{Alias: "lab"})
	require.Equal(t, screenSet, m.screen)
	require.NotNil(t, cmd)

	_, stale := send(m, setPollMsg{Gen: beat})
	require.Nil(t, stale, "the beat of the poll that ended with the tree is dropped")
	setGen := m.setGen
	m, wait := send(m, setPollMsg{Gen: m.setPollGen})
	require.NotNil(t, wait, "the poll waits a beat for the new server")
	require.Equal(t, setGen, m.setGen, "nothing is read before the new server connects")
	require.Equal(t, "pid 77 on desk, since 09:15", m.setView.elsewhere)

	m.svc = svc
	_, load := send(m, setPollMsg{Gen: m.setPollGen})
	require.NotNil(t, load, "the set is re-read once the new server connects")
}

// The variables prompt is a screen of its own over the set; leaving it for
// the set must restart the poll the same way.
func TestWayBackFromTheSetVariablesRestartsThePoll(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m.setView.elsewhere = "pid 77 on desk, since 09:15"
	beat := m.setPollGen // the poll's beat in flight on the set screen
	m.show(screenSetVars)
	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen)
	require.NotNil(t, cmd, "the set's poll starts again")

	_, stale := send(m, setPollMsg{Gen: beat})
	require.Nil(t, stale, "the beat of the poll that ended with the prompt is dropped")
	_, load := send(m, setPollMsg{Gen: m.setPollGen})
	require.NotNil(t, load, "the new loop's beat re-reads the set")
}

// Showing a screen starts its loops; leaving it ends them at once, so a beat
// that lands off its screen is dropped and never a second loop runs.
func TestShowOwnsEachScreensLoops(t *testing.T) {
	m := setsModel(t)
	m.home.lastKey = m.now()

	require.NotNil(t, m.show(screenHome), "Home starts its refresh")
	home := homeTickMsg{Gen: m.home.tickGen}
	_, beat := send(m, home)
	require.NotNil(t, beat, "Home's current beat reloads the plans")
	m.show(screenPlan)
	_, beat = send(m, home)
	require.Nil(t, beat, "leaving Home ends its refresh: its beat is dropped")

	require.NotNil(t, m.show(screenBoard), "the board starts its beats")
	board := boardTickMsg{Gen: m.board.tickGen}
	_, beat = send(m, board)
	require.NotNil(t, beat, "the board's current beat keeps it reading")
	m.show(screenLogs)
	_, beat = send(m, board)
	require.Nil(t, beat, "leaving the board ends its beats")

	m.setView.elsewhere = "pid 77 on desk, since 09:15"
	require.NotNil(t, m.show(screenSet), "a set held elsewhere is polled")
	poll := setPollMsg{Gen: m.setPollGen}
	_, beat = send(m, poll)
	require.NotNil(t, beat, "the set's current beat re-reads the set")
	m.show(screenTree)
	_, beat = send(m, poll)
	require.Nil(t, beat, "leaving the set ends its poll")
	require.Nil(t, m.show(screenForm), "a screen with no loop starts none")
}

// show is the only writer of the screen: any other assignment would skip
// the loops the screen owns.
func TestShowIsTheOnlyWriterOfTheScreen(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || (e.Name() == "show.go" && fn.Name.Name == "show") {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if as, ok := n.(*ast.AssignStmt); ok {
					for _, l := range as.Lhs {
						if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "screen" {
							t.Errorf("%s: %s sets the screen; use show", e.Name(), fn.Name.Name)
						}
					}
				}
				return true
			})
		}
	}
}
