package tui

import tea "github.com/charmbracelet/bubbletea"

// navFrame is one screen esc can return to. The way back is a stack of them:
// a screen that opens another pushes itself, esc pops, and a jump to a root
// screen (Home, the board, a set) resets the stack. Nothing else remembers
// where esc goes.
type navFrame struct {
	screen screen

	// plan, for a plan screen: the screen to draw again, with its cursors.
	// Nil means the plan screen as the model holds it now, which a log screen
	// or the run form was opened over.
	plan *planFrame
	// clean says the plan screen is drawn afresh, with no cache, as it was
	// after a tree screen. A drilled plan screen keeps its cache.
	clean bool
	// tree, for a tree screen: the screen to draw again. Nil means the tree
	// screen as the model holds it now. It is set once another tree screen
	// takes its place.
	tree *treeScreen
	// projects, for Home: the plans table was reached from the projects
	// table, so esc there returns to it.
	projects bool
}

// navStack is the screens below the current one, the nearest last. Every
// operation returns a stack of its own, so models that share one never
// write to each other's.
type navStack []navFrame

func (s *navStack) push(f navFrame) {
	*s = append((*s)[:len(*s):len(*s)], f)
}

func (s *navStack) pop() (navFrame, bool) {
	n := len(*s)
	if n == 0 {
		return navFrame{}, false
	}
	f := (*s)[n-1]
	*s = (*s)[: n-1 : n-1]
	return f, true
}

func (s navStack) top() (navFrame, bool) {
	if len(s) == 0 {
		return navFrame{}, false
	}
	return s[len(s)-1], true
}

// resetTo replaces the stack with frames, for a jump to a screen that has no
// way back of its own.
func (s *navStack) resetTo(frames ...navFrame) {
	*s = append(navStack(nil), frames...)
}

// trail says Home's plans table was reached from the projects table.
func (s navStack) trail() bool {
	return len(s) > 0 && s[0].screen == screenHome && s[0].projects
}

// toHome keeps only the trail to the projects table: Home is the root, and
// what was opened from it is gone.
func (s *navStack) toHome() {
	if s.trail() {
		s.resetTo((*s)[0])
		return
	}
	s.resetTo()
}

// dropTrail forgets the way back to the projects table and keeps the rest.
func (s *navStack) dropTrail() {
	if s.trail() {
		s.resetTo((*s)[1:]...)
	}
}

// rootedOnBoard says the board is the bottom of the way back.
func (s navStack) rootedOnBoard() bool {
	return len(s) > 0 && s[0].screen == screenBoard
}

// pinTree keeps the tree screen on show for the nearest tree frame below a
// plan screen that is about to open another tree: that frame draws it again
// later. Plan screens drilled from the one opened on the tree may sit between.
func (s *navStack) pinTree(cur treeScreen) {
	for i := len(*s) - 1; i >= 0; i-- {
		if (*s)[i].screen != screenTree {
			continue
		}
		if (*s)[i].tree == nil {
			next := append(navStack(nil), *s...)
			next[i].tree = &cur
			*s = next
		}
		return
	}
}

// here is the frame that returns to the screen the person is on now.
func (m Model) here() navFrame {
	return navFrame{screen: m.screen}
}

// openedFrom records that the screen shown now is where esc returns to from
// whatever opens next.
func (m *Model) openedFrom() { m.nav.push(m.here()) }

// homeBase makes Home the way back: what was opened from Home or a plan
// screen is gone, and so is any trail but the one to the projects table.
func (m *Model) homeBase() {
	m.nav.toHome()
	m.nav.push(navFrame{screen: screenHome})
}

// popNav is esc leaving the screen on top: it shows the screen below it, or
// Home when nothing is below.
func (m Model) popNav() (tea.Model, tea.Cmd) {
	f, ok := m.nav.pop()
	if !ok {
		return m.goHome()
	}
	switch f.screen {
	case screenHome:
		if m.screen == screenTree || m.screen == screenLogs {
			m.closePlan() // a build opened on the tree left its plan screen behind
		}
		if f.projects {
			return m.backToProjects()
		}
		return m.goHome()
	case screenBoard:
		return m.showBoard()
	case screenSet:
		// Closing the plan screen stops its watch and drops its loads still
		// in flight, so none lands on a plan the user has left.
		m.closePlan()
		cmd := m.show(screenSet)
		return m, cmd
	case screenTree:
		m.closePlan()
		if f.tree != nil {
			m.buildTree = *f.tree
			m.buildTree.restarting = nil
		}
		m.show(screenTree)
		return m.watchTree(false) // watching paused while the build was open
	case screenPlan:
		if f.plan == nil {
			m.show(screenPlan) // the plan screen is still the model's
			return m, nil
		}
		var cmd tea.Cmd
		if f.clean {
			m, cmd = m.enterPlan(f.plan.key, f.plan.buildsPlan, f.plan.wantOf(), f.plan.focus)
		} else {
			m, cmd = m.openPlan(f.plan.key, f.plan.buildsPlan, f.plan.wantOf(), f.plan.focus)
		}
		m.restoreCursors(*f.plan)
		return m, cmd
	}
	cmd := m.show(f.screen)
	return m, cmd
}
