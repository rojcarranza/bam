package tui

import (
	"fmt"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
)

// pinSel is one line the cursor of Pinned can stop on: a pin's own row
// (node nil) or a pipeline of its tree.
type pinSel struct {
	pin  app.PinView
	node *app.TreeNode
}

// noText is the text of a tree row when only its shape matters.
func noText(*app.TreeNode, bool) string { return "" }

// pinTreeRows is the rows under a pin's own row: its tree as bam build tree
// draws it, the root left out since the pin's row stands for it.
func pinTreeRows(p app.PinView, text func(*app.TreeNode, bool) string) []treeLine {
	if !p.HasChildren() {
		return nil
	}
	return treeRows(p.Tree.Root, text)[1:]
}

// pinSels is every line the cursor can stop on, in screen order: each pin,
// then the pipelines of its tree unless it is folded.
func (b *boardState) pinSels() []pinSel {
	var out []pinSel
	for _, g := range b.data.Groups {
		for _, p := range g.Pins {
			out = append(out, pinSel{pin: p})
			if !b.folded[p.Pin] {
				for _, l := range pinTreeRows(p, noText) {
					if l.node != nil && !l.node.NotRun {
						out = append(out, pinSel{pin: p, node: l.node})
					}
				}
			}
			for _, br := range p.Branches {
				out = append(out, pinSel{pin: br})
			}
		}
	}
	return out
}

// pinPipelines counts the pipelines below a pin's own.
func pinPipelines(p app.PinView) int {
	n := 0
	for _, l := range pinTreeRows(p, noText) {
		if l.node != nil && !l.node.NotRun {
			n++
		}
	}
	return n
}

// pinFoldable says whether a pin's tree is finished and every pipeline in it
// succeeded; the board folds such a tree first when Pinned is too short.
func pinFoldable(p app.PinView) bool {
	return p.HasChildren() && app.TreeFinal(*p.Tree) && p.Tree.State == "success"
}

// pinPart is one pin as Pinned draws it: its row and the lines of its tree.
type pinPart struct {
	group   int
	row     string
	rowOrd  int
	tree    []string
	tail    []string // the branch rows under the pin and the line counting the rest
	tailOrd []int
	ord     []int // the cursor ordinal of each tree line, -1 for a stage line
	// fold is set when the tree shows as one line; shown is how many tree
	// lines are kept when it is cut.
	fold  bool
	shown int
	cut   bool
	count int
}

// treeLineText is how a pipeline of a pin's tree reads: its tree line, and
// the stage it is in when it runs or failed.
func (m Model) treeLineText(n *app.TreeNode, underStage bool) string {
	text := m.treeNodeText(n, underStage)
	if st := app.StageProgress(n.Build); st != "" && !n.NotRun && !n.Loop && !n.Superseded {
		text += "  " + st
	}
	return text
}

// pinLines draws the groups, and says which line holds the cursor. capLines,
// when over 0, is how many lines Pinned has: when the trees do not fit, the
// trees that are finished and all succeeded fold to one line each, then the
// longest tree is cut and ends in a line saying how many are left out. A
// tree holding the cursor is never folded or cut past the cursor. With
// roomy, a blank line separates the groups when that fits without a cut.
func (m Model) pinLines(inner int, roomy bool, capLines int) ([]string, int) {
	b := &m.board
	if !b.have {
		return []string{dimStyle.Render("  reading the pipelines…")}, 0
	}
	if len(b.data.Groups) == 0 {
		return []string{dimStyle.Render("  No pipelines pinned. Pin one with: bam pin add GROUP PLAN")}, 0
	}
	nowW := m.pinNowWidth()
	labelW := m.pinLabelWidth(inner, nowW)
	focused := b.panel == boardPins
	cur := b.cursor[boardPins]
	var parts []*pinPart
	n := 0
	for gi, g := range b.data.Groups {
		for _, p := range g.Pins {
			part := &pinPart{group: gi, rowOrd: n, row: m.cursorFor(focused, n == cur) + m.pinText(p, "", labelW, nowW)}
			n++
			if !b.folded[p.Pin] {
				for _, l := range pinTreeRows(p, m.treeLineText) {
					sel := l.node != nil && !l.node.NotRun
					ord := -1
					if sel {
						ord = n
						n++
					}
					part.tree = append(part.tree, m.cursorFor(focused && sel, sel && ord == cur)+"  "+l.text)
					part.ord = append(part.ord, ord)
				}
				part.shown = len(part.tree)
			} else if p.HasChildren() {
				part.fold, part.count = true, pinPipelines(p)
			}
			for _, br := range p.Branches {
				part.tail = append(part.tail, m.cursorFor(focused, n == cur)+m.pinText(br, "  ", labelW, nowW))
				part.tailOrd = append(part.tailOrd, n)
				n++
			}
			if p.BranchesMore > 0 {
				part.tail = append(part.tail, dimStyle.Render(fmt.Sprintf("    +%d more branches", p.BranchesMore)))
				part.tailOrd = append(part.tailOrd, -1)
			}
			parts = append(parts, part)
		}
	}
	// The pins are in group order; index them again for the fold rules.
	pins := make([]app.PinView, 0, len(parts))
	for _, g := range b.data.Groups {
		pins = append(pins, g.Pins...)
	}
	holds := func(i int) int { // the index of the tree line holding the cursor, or -1
		for j, o := range parts[i].ord {
			if o == cur {
				return j
			}
		}
		return -1
	}
	total := func() int {
		t := len(b.data.Groups)
		for _, p := range parts {
			t += 1 + p.height() + len(p.tail)
		}
		return t
	}
	trimmed := false
	if capLines > 0 {
		for i, p := range parts {
			if total() <= capLines {
				break
			}
			if !p.fold && len(p.tree) > 0 && pinFoldable(pins[i]) && holds(i) < 0 && !b.opened[pins[i].Pin] {
				p.fold, p.count, trimmed = true, pinPipelines(pins[i]), true
			}
		}
		for total() > capLines {
			best, gain := -1, 0
			for i, p := range parts {
				if p.fold || p.cut {
					continue
				}
				keep := holds(i) + 1
				if g := len(p.tree) - keep - 1; g > gain || (g == gain && g > 0 && len(p.tree) > len(parts[best].tree)) {
					best, gain = i, g
				}
			}
			if best < 0 {
				break
			}
			p := parts[best]
			excess := total() - capLines
			p.shown = max(len(p.tree)-excess-1, holds(best)+1)
			p.cut, trimmed = true, true
		}
	}
	if b.fitFolded == nil {
		b.fitFolded = map[string]bool{}
	}
	clear(b.fitFolded)
	for i, p := range parts {
		if p.fold && !b.folded[pins[i].Pin] {
			b.fitFolded[pins[i].Pin] = true
		}
	}
	spaced := roomy && !trimmed && capLines > 0 && total()+len(b.data.Groups)-1 <= capLines

	var out []string
	cursorLine := 0
	group := -1
	for _, p := range parts {
		if p.group != group {
			if spaced && group >= 0 {
				out = append(out, "")
			}
			group = p.group
			out = append(out, "  "+titleStyle.Render(b.data.Groups[group].Name))
		}
		if p.rowOrd == cur {
			cursorLine = len(out)
		}
		out = append(out, p.row)
		switch {
		case p.fold:
			out = append(out, dimStyle.Render(fmt.Sprintf("      ▸ %s · space opens", pipelinesText(p.count))))
		default:
			for j, l := range p.tree[:p.shown] {
				if p.ord[j] == cur {
					cursorLine = len(out)
				}
				out = append(out, l)
			}
			if p.cut {
				out = append(out, dimStyle.Render(fmt.Sprintf("      +%d more · t opens the tree", len(p.tree)-p.shown)))
			}
		}
		for j, l := range p.tail {
			if p.tailOrd[j] == cur {
				cursorLine = len(out)
			}
			out = append(out, l)
		}
	}
	return out, cursorLine
}

func pipelinesText(n int) string {
	if n == 1 {
		return "1 child pipeline"
	}
	return fmt.Sprintf("%d child pipelines", n)
}

// height is the lines a pin's tree takes below its row.
func (p *pinPart) height() int {
	switch {
	case p.fold:
		return 1
	case p.cut:
		return p.shown + 1
	}
	return len(p.tree)
}

// toggleFold is space: fold or unfold the tree of the pin under the cursor.
func (m Model) toggleFold() Model {
	b := &m.board
	sels := b.pinSels()
	c := b.cursor[boardPins]
	if b.panel != boardPins || c >= len(sels) || !sels[c].pin.HasChildren() {
		return m
	}
	pin := sels[c].pin
	folded := map[string]bool{}
	for k, v := range b.folded {
		folded[k] = v
	}
	opened := map[string]bool{}
	for k, v := range b.opened {
		opened[k] = v
	}
	switch {
	case b.fitFolded[pin.Pin]:
		// Folded only to fit the panel: space opens it, and the fit rule leaves it open.
		opened[pin.Pin] = true
	case folded[pin.Pin]:
		delete(folded, pin.Pin)
	default:
		delete(opened, pin.Pin)
		folded[pin.Pin] = true
		// The cursor was on a line of the tree that is gone: back to the pin.
		for i, s := range sels {
			if s.node == nil && s.pin.Pin == pin.Pin {
				b.cursor[boardPins] = i
				break
			}
		}
	}
	b.folded, b.opened = folded, opened
	b.clamp()
	return m
}

// pinSelKey names the line of Pinned the cursor is on, so it can be found
// again when a refresh adds or removes lines above it: the pin, the plan of a
// branch row, and the build of a pipeline of a tree.
func (b *boardState) pinSelKey() string {
	sels := b.pinSels()
	c := b.cursor[boardPins]
	if b.panel != boardPins || c >= len(sels) {
		return "" // a cursor nobody is looking at is left to the clamp
	}
	s := sels[c]
	key := s.pin.Pin + "|" + s.pin.PlanKey + "|" + s.pin.Branch
	if s.node != nil {
		key += "|" + s.node.Build.Key
	}
	return key
}

// followPinSel moves the cursor of Pinned to the line pinSelKey named, or to
// the pin's own row when its tree line is gone; it stays where it is when
// neither is there.
func (b *boardState) followPinSel(key string) {
	if key == "" {
		return
	}
	pinKey := key[:strings.LastIndex(key, "|")+1]
	fallback := -1
	for i, s := range b.pinSels() {
		k := s.pin.Pin + "|" + s.pin.PlanKey + "|" + s.pin.Branch
		if s.node != nil {
			k += "|" + s.node.Build.Key
		}
		if k == key {
			b.cursor[boardPins] = i
			return
		}
		if s.node == nil && k+"|" == pinKey {
			fallback = i
		}
	}
	if fallback >= 0 {
		b.cursor[boardPins] = fallback
	}
}
