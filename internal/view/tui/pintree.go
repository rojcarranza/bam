package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/view"
)

// linkSpan is a stretch of a Pinned line a click opens in Bamboo: columns
// x0 up to x1 of the line, and the key whose Bamboo page it opens (a plan
// key for a name, a build key for a number).
type linkSpan struct {
	x0, x1 int
	key    string
}

// pinLineInfo says what a line of Pinned is for the mouse: the cursor
// ordinal of the line it selects (-1 when it selects nothing) and the
// underlined stretches that open a page.
type pinLineInfo struct {
	ord   int
	spans []linkSpan
}

// linkText underlines s, the way names and numbers that open in Bamboo show.
// lipgloss styles each character alone for an underline, so the escape codes
// are taken from a one-character sample and put round the whole text; with no
// colour support the sample comes back bare and s stays plain.
func linkText(s string) string {
	if s == "" {
		return s
	}
	pre, post, ok := strings.Cut(linkStyle.Render("x"), "x")
	if !ok {
		return s
	}
	return pre + s + post
}

// pinNodeText is a pipeline of a pin's tree as treeNodeText draws it, with
// its plan name and its build number underlined, and the stretches of the
// text (counted from its first column) that open the plan and the build.
func (m Model) pinNodeText(n *app.TreeNode, underStage bool) (string, []linkSpan) {
	text := m.treeNodeText(n, underStage)
	if n.NotRun || n.Loop || n.Superseded {
		return text, nil
	}
	b := n.Build
	name := view.TreePlanName(b)
	head := b.Key + name
	if !strings.HasPrefix(text, head) {
		return text, nil
	}
	planPart, num := b.Key, ""
	if i := strings.LastIndex(b.Key, "-"); i > 0 {
		planPart, num = b.Key[:i], b.Key[i+1:]
	}
	planKey := b.PlanKey
	if planKey == "" {
		planKey = planPart
	}
	var out strings.Builder
	var spans []linkSpan
	x := 0
	add := func(plain string, key string, link bool) {
		w := lipgloss.Width(plain)
		if link && key != "" && w > 0 {
			spans = append(spans, linkSpan{x, x + w, key})
			out.WriteString(linkText(plain))
		} else {
			out.WriteString(plain)
		}
		x += w
	}
	add(planPart, planKey, true)
	if num != "" {
		add("-", "", false)
		add(num, b.Key, true)
	}
	if name != "" {
		add(" ", "", false)
		add(strings.TrimPrefix(name, " "), planKey, true)
	}
	out.WriteString(text[len(head):])
	return out.String(), spans
}

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
	return p.HasChildren() && p.Tree.Settled() && p.Tree.State == "success"
}

// pinPart is one pin as Pinned draws it: its row and the lines of its tree.
type pinPart struct {
	group   int
	row     string
	record  string // the muted pass-record line under the row, "" for none
	rowSp   []linkSpan
	rowOrd  int
	tree    []string
	treeSp  [][]linkSpan
	tail    []string // the branch rows under the pin and the line counting the rest
	tailSp  [][]linkSpan
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
	text, _ := m.pinNodeText(n, underStage)
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
	lines, cur, _ := m.pinLinesInfo(inner, roomy, capLines)
	return lines, cur
}

// pinLinesInfo is pinLines with what the mouse needs: for each line, which
// row of Pinned it selects and which stretches open a page.
func (m Model) pinLinesInfo(inner int, roomy bool, capLines int) ([]string, int, []pinLineInfo) {
	b := &m.board
	if !b.have {
		return []string{dimStyle.Render("  reading the pipelines…")}, 0, []pinLineInfo{{ord: -1}}
	}
	if len(b.data.Groups) == 0 {
		return []string{dimStyle.Render("  No pipelines pinned. Pin one with: bam pin add GROUP PLAN")}, 0, []pinLineInfo{{ord: -1}}
	}
	cols := m.pinColumns(inner)
	focused := b.panel == boardPins
	cur := b.cursor[boardPins]
	var parts []*pinPart
	n := 0
	for gi, g := range b.data.Groups {
		for _, p := range g.Pins {
			rowText, rowSp := m.pinTextSpans(p, "", cols)
			part := &pinPart{group: gi, rowOrd: n, row: m.cursorFor(focused, n == cur) + rowText, rowSp: shiftSpans(rowSp, 2)}
			n++
			if p.Err == nil {
				if rec := view.PassRecord(p.Results, m.now()); rec != "" {
					part.record = dimStyle.Render("    " + rec)
				}
			}
			if !b.folded[p.Pin] {
				for _, l := range pinTreeRows(p, m.treeLineText) {
					sel := l.node != nil && !l.node.NotRun
					ord := -1
					if sel {
						ord = n
						n++
					}
					part.tree = append(part.tree, m.cursorFor(focused && sel, sel && ord == cur)+"  "+l.text)
					part.treeSp = append(part.treeSp, m.treeLineSpans(l, 4))
					part.ord = append(part.ord, ord)
				}
				part.shown = len(part.tree)
			} else if p.HasChildren() {
				part.fold, part.count = true, pinPipelines(p)
			}
			for _, br := range p.Branches {
				brText, brSp := m.pinTextSpans(br, "  ", cols)
				part.tail = append(part.tail, m.cursorFor(focused, n == cur)+brText)
				part.tailSp = append(part.tailSp, shiftSpans(brSp, 2))
				part.tailOrd = append(part.tailOrd, n)
				n++
			}
			if p.BranchesMore > 0 {
				part.tail = append(part.tail, dimStyle.Render(fmt.Sprintf("    +%d more branches", p.BranchesMore)))
				part.tailSp = append(part.tailSp, nil)
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
			t += 1 + p.recordLines() + p.height() + len(p.tail)
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
	var info []pinLineInfo
	put := func(line string, ord int, spans []linkSpan) {
		out = append(out, line)
		info = append(info, pinLineInfo{ord: ord, spans: spans})
	}
	cursorLine := 0
	group := -1
	for _, p := range parts {
		if p.group != group {
			if spaced && group >= 0 {
				put("", -1, nil)
			}
			group = p.group
			g := b.data.Groups[group]
			put("  "+view.RuledLine(view.GroupTitle(g.Name), view.GroupState(g), max(inner-4, 1), func(s string) string { return titleStyle.Render(s) }, func(s string) string { return dimStyle.Render(s) }, func(s string) string { return dimStyle.Render(s) }), -1, nil)
		}
		if p.rowOrd == cur {
			cursorLine = len(out)
		}
		put(p.row, p.rowOrd, p.rowSp)
		if p.record != "" {
			put(p.record, -1, nil)
		}
		switch {
		case p.fold:
			put(dimStyle.Render(fmt.Sprintf("      ▸ %s · space opens", pipelinesText(p.count))), -1, nil)
		default:
			for j, l := range p.tree[:p.shown] {
				if p.ord[j] == cur {
					cursorLine = len(out)
				}
				put(l, p.ord[j], p.treeSp[j])
			}
			if p.cut {
				put(dimStyle.Render(fmt.Sprintf("      +%d more · t opens the tree", len(p.tree)-p.shown)), -1, nil)
			}
		}
		for j, l := range p.tail {
			if p.tailOrd[j] == cur {
				cursorLine = len(out)
			}
			put(l, p.tailOrd[j], p.tailSp[j])
		}
	}
	return out, cursorLine, info
}

// shiftSpans moves spans right by dx columns.
func shiftSpans(sp []linkSpan, dx int) []linkSpan {
	out := make([]linkSpan, len(sp))
	for i, s := range sp {
		out[i] = linkSpan{s.x0 + dx, s.x1 + dx, s.key}
	}
	return out
}

// treeLineSpans is the stretches of a tree line (drawn after lead columns of
// cursor and indent) that open Bamboo: those of the pipeline's text, found
// where its build key starts. A stage line has none.
func (m Model) treeLineSpans(l treeLine, lead int) []linkSpan {
	if l.node == nil || l.node.NotRun {
		return nil
	}
	_, sp := m.pinNodeText(l.node, false)
	if len(sp) == 0 {
		return nil
	}
	plainText := ansi.Strip(l.text)
	i := strings.Index(plainText, l.node.Build.Key)
	if i < 0 {
		return nil
	}
	return shiftSpans(sp, lead+lipgloss.Width(plainText[:i]))
}

func pipelinesText(n int) string {
	if n == 1 {
		return "1 child pipeline"
	}
	return fmt.Sprintf("%d child pipelines", n)
}

// recordLines is 1 when the pin has a pass-record line under its row.
func (p *pinPart) recordLines() int {
	if p.record != "" {
		return 1
	}
	return 0
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
