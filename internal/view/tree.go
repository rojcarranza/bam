package view

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// treeBad reports whether a node failed, was stopped or did not run.
func treeBad(n *app.TreeNode) bool {
	s := n.Build.State
	return n.NotRun || (app.TreeFinished(s) && s != provider.StateSuccess && s != provider.StateSkipped)
}

// pruneFailed keeps the nodes on a path to a bad node; the root always stays.
func pruneFailed(n *app.TreeNode) *app.TreeNode {
	c := *n
	c.Children = nil
	for _, k := range n.Children {
		if k.Superseded {
			continue
		}
		if p := pruneFailed(k); treeBad(k) || len(p.Children) > 0 {
			c.Children = append(c.Children, p)
		}
	}
	return &c
}

// TreeText prints a build tree: a line per build, its stages under it, then
// its children; then one failed: line per build that did not succeed, with
// its URL, and one next: line per failed build with a failed job.
func TreeText(o Out, t app.Tree, failedOnly bool) error {
	root := t.Root
	if failedOnly {
		root = pruneFailed(root)
	}
	showStages := !failedOnly || len(root.Children) > 0 || treeBad(root)
	tb := Table{Headers: []string{"", "", "", ""}}
	tb.Rows = append(tb.Rows, treeRow(o, root, "", false))
	var failed, next []string
	if treeBad(root) && !root.NotRun {
		failed = append(failed, treeFailedLine(root.Build))
	}
	if root.Build.State == provider.StateFailed && len(root.Build.FailedJobs()) > 0 {
		next = append(next, root.Build.Key)
	}
	var add func(n *app.TreeNode, indent string, stages bool)
	// node is one build's line, and everything under it.
	node := func(k *app.TreeNode, indent, branch, more string, underStage bool) {
		tb.Rows = append(tb.Rows, treeRow(o, k, indent+branch, underStage))
		if k.Superseded {
			return
		}
		if !k.NotRun && !k.Loop && treeBad(k) {
			failed = append(failed, treeFailedLine(k.Build))
		}
		if !k.NotRun && !k.Loop && k.Build.State == provider.StateFailed && len(k.Build.FailedJobs()) > 0 {
			next = append(next, k.Build.Key)
		}
		add(k, indent+more, true)
	}
	add = func(n *app.TreeNode, indent string, stages bool) {
		items := TreeItems(n, stages && !n.NotRun && !n.Loop && !n.Superseded)
		for i, it := range items {
			branch, more := "├─ ", "│  "
			if i == len(items)-1 {
				branch, more = "└─ ", "   "
			}
			if it.Stage == nil {
				node(it.Node, indent, branch, more, false)
				continue
			}
			tb.Rows = append(tb.Rows, stageRow(o, n.Build, *it.Stage, indent+branch))
			for j, k := range it.Under {
				b, m := "├─ ", "│  "
				if j == len(it.Under)-1 {
					b, m = "└─ ", "   "
				}
				node(k, indent+more, b, m, true)
			}
		}
	}
	add(root, "", showStages)
	if err := RenderRows(o, tb); err != nil {
		return err
	}
	for _, l := range failed {
		if _, err := fmt.Fprintln(o.W, l); err != nil {
			return err
		}
	}
	for _, k := range next {
		if _, err := fmt.Fprintf(o.W, "next: bam logs %s --failed\n", k); err != nil {
			return err
		}
	}
	return nil
}

// treeFailedLine names a build that did not succeed and where to open it:
// "failed: OPS-SMOKE-51 https://bamboo.example.com/browse/OPS-SMOKE-51".
func treeFailedLine(b provider.Build) string {
	return strings.TrimSpace(style.Label(b.State) + ": " + b.Key + " " + b.URL)
}

// TreeItem is one line under a build in a tree: a stage, with the builds
// its jobs started under it, or a child build.
type TreeItem struct {
	Stage *provider.Stage
	Under []*app.TreeNode // builds the stage's jobs started, linked from their logs
	Node  *app.TreeNode
}

// TreeItems is what sits under n: with stages, each stage followed by the
// builds its jobs started, then every other child; without, the children.
func TreeItems(n *app.TreeNode, stages bool) []TreeItem {
	var items []TreeItem
	placed := map[*app.TreeNode]bool{}
	if stages {
		for i := range n.Build.Stages {
			it := TreeItem{Stage: &n.Build.Stages[i]}
			for _, k := range n.Children {
				if k.FromLog && k.Stage == it.Stage.Name && !placed[k] {
					it.Under = append(it.Under, k)
					placed[k] = true
				}
			}
			items = append(items, it)
		}
	}
	for _, k := range n.Children {
		if !placed[k] {
			items = append(items, TreeItem{Node: k})
		}
	}
	return items
}

// treeRow is one build's line. underStage: it is drawn under the stage that
// started it, which then need not be named again.
func treeRow(o Out, n *app.TreeNode, prefix string, underStage bool) []string {
	switch {
	case n.Superseded:
		return []string{prefix + style.Dim(o.Style, n.Build.Key), style.Dim(o.Style, style.Glyph(n.Build.State)+" "+style.Label(n.Build.State)),
			style.Dim(o.Style, Duration(n.Build.Duration)), style.Dim(o.Style, "earlier run")}
	case n.NotRun:
		return []string{prefix + n.PlanKey, style.Pending(o.Style) + " not run", "", ""}
	case n.Loop:
		return []string{prefix + o.Key(n.Build.Key, n.Build.URL) + TreePlanName(n.Build), "↺ already shown above", "", ""}
	}
	return []string{prefix + o.Key(n.Build.Key, n.Build.URL) + TreePlanName(n.Build), o.State(n.Build.State), Duration(elapsed(o, n.Build)), TreeNodeNote(n, underStage)}
}

// TreePlanName is what follows a build's key on its tree line: its
// pipeline's name, and its branch for a branch build, " (Build and test ·
// develop)"; empty when the provider gave no name.
func TreePlanName(b provider.Build) string {
	switch {
	case b.PlanName == "":
		return ""
	case b.Branch != "":
		return " (" + b.PlanName + " · " + b.Branch + ")"
	}
	return " (" + b.PlanName + ")"
}

// TreeNodeNote is what a build's tree line says after its duration: the
// stage whose job started it, for a build linked from a log that is not
// drawn under that stage, and its count of failed tests.
func TreeNodeNote(n *app.TreeNode, underStage bool) string {
	var parts []string
	if n.FromLog && n.Stage != "" && !underStage {
		parts = append(parts, "started by stage "+n.Stage)
	}
	if c := len(n.Build.FailedTests); c > 0 {
		parts = append(parts, fmt.Sprintf("%d tests failed", c))
	}
	return strings.Join(parts, " · ")
}

func stageRow(o Out, b provider.Build, s provider.Stage, prefix string) []string {
	extra := ""
	if s.State == provider.StateFailed {
		var names []string
		for _, j := range s.Jobs {
			if j.State == provider.StateFailed {
				names = append(names, j.Name)
			}
		}
		extra = strings.Join(names, ", ")
	}
	return []string{prefix + "stage " + s.Name, o.State(s.State), Duration(s.Duration), extra}
}

type TreeDoc struct {
	State    string      `json:"state"`
	Warnings []string    `json:"warnings"`
	Root     TreeNodeDoc `json:"root"`
}

type TreeNodeDoc struct {
	PlanKey  string        `json:"plan_key"`
	Status   string        `json:"status"`
	FromLog  bool          `json:"from_log"`        // linked by its parent's job log, not its reason
	Stage    string        `json:"stage,omitempty"` // for from_log, the parent's stage that started it
	Build    *BuildDoc     `json:"build"`
	Children []TreeNodeDoc `json:"children"`
}

// TreeJSON is bam build tree --json.
func TreeJSON(t app.Tree, failedOnly bool) TreeDoc {
	root := t.Root
	if failedOnly {
		root = pruneFailed(root)
	}
	return TreeDoc{State: string(t.State), Warnings: nonNil(t.Warnings), Root: treeNodeDoc(root)}
}

func treeNodeDoc(n *app.TreeNode) TreeNodeDoc {
	d := TreeNodeDoc{PlanKey: n.PlanKey, Status: string(n.Build.State), FromLog: n.FromLog, Stage: n.Stage, Children: []TreeNodeDoc{}}
	switch {
	case n.NotRun:
		d.Status = "not_run"
		return d
	case n.Loop:
		d.Status = "loop"
	case n.Superseded:
		d.Status = "superseded"
	}
	b := BuildJSON(n.Build)
	d.Build = &b
	if n.Loop {
		return d
	}
	for _, k := range n.Children {
		d.Children = append(d.Children, treeNodeDoc(k))
	}
	return d
}

// TreeRenderer shows a watched tree.
type TreeRenderer interface {
	Event(e app.TreeEvent)
}

// TreeProgressText says what a tree load is reading: every phase that has
// started and not finished, or the last one started once all have.
func TreeProgressText(p app.TreeProgress) string {
	phases := []struct {
		c          app.TreeCount
		verb, unit string
	}{
		{p.Projects, "listing plans", "projects"},
		{p.Plans, "reading builds", "plans"},
		{p.Builds, "reading details", "builds"},
		{p.Jobs, "reading logs", "jobs"},
	}
	var open []string
	last := ""
	for _, ph := range phases {
		if ph.c.Total == 0 {
			continue
		}
		s := fmt.Sprintf("%s %d/%d %s", ph.verb, ph.c.Done, ph.c.Total, ph.unit)
		last = s
		if ph.c.Done < ph.c.Total {
			open = append(open, s)
		}
	}
	switch {
	case len(open) > 0:
		return strings.Join(open, " · ")
	case last != "":
		return last
	}
	return "reading the tree"
}

// TreeLive redraws the tree in place on a TTY, and shows the first
// refresh's progress on its own line until the tree arrives.
type TreeLive struct {
	o        Out
	failed   bool
	lines    int
	progress *ProgressLine
}

// NewTreeLive draws on o. Progress goes to progress, usually stderr; nil
// shows none.
func NewTreeLive(o Out, progress io.Writer, failedOnly bool) *TreeLive {
	l := &TreeLive{o: o, failed: failedOnly}
	if progress != nil {
		l.progress = NewProgressLine(progress, o.Width)
	}
	return l
}

// Stop clears the progress line, for a watch that ends without a tree.
func (l *TreeLive) Stop() {
	if l != nil {
		l.progress.Clear()
	}
}

func (l *TreeLive) Event(e app.TreeEvent) {
	if e.Type == app.TreeLoading {
		l.progress.Show(TreeProgressText(e.Progress))
		return
	}
	l.progress.Clear()
	if e.Type == app.TreeError {
		return
	}
	var b strings.Builder
	o := l.o
	o.W = &b
	_ = TreeText(o, e.Tree, l.failed)
	text := b.String()
	if l.lines > 0 {
		fmt.Fprintf(l.o.W, "\x1b[%dA\x1b[J", l.lines)
	}
	fmt.Fprint(l.o.W, text)
	l.lines = strings.Count(text, "\n")
}

// TreeLines prints one timestamped line per node whose state changed, for
// pipes and CI logs, and the whole tree at the end.
type TreeLines struct {
	o      Out
	failed bool
	seen   map[string]string
}

func NewTreeLines(o Out, failedOnly bool) *TreeLines {
	return &TreeLines{o: o, failed: failedOnly, seen: map[string]string{}}
}

func (l *TreeLines) Event(e app.TreeEvent) {
	switch e.Type {
	case app.TreeUpdate:
		ts := e.Time.UTC().Format(time.RFC3339)
		e.Tree.Root.Walk(func(n *app.TreeNode, _ int) {
			key, state := n.Build.Key, string(n.Build.State)
			switch {
			case n.NotRun:
				key, state = n.PlanKey, "not run"
			case n.Loop:
				return
			}
			if l.seen[key] == state {
				return
			}
			l.seen[key] = state
			fmt.Fprintf(l.o.W, "%s %s %s\n", ts, key, state)
		})
	case app.TreeDone:
		_ = TreeText(l.o, e.Tree, l.failed)
	}
}

// TreeNDJSON writes one tree document per update.
type TreeNDJSON struct {
	o      Out
	failed bool
}

func NewTreeNDJSON(o Out, failedOnly bool) *TreeNDJSON { return &TreeNDJSON{o: o, failed: failedOnly} }

func (n *TreeNDJSON) Event(e app.TreeEvent) {
	if e.Type == app.TreeUpdate {
		_ = WriteNDJSON(n.o.W, TreeJSON(e.Tree, n.failed))
	}
}
