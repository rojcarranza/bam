package view

import (
	"fmt"
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
// its children; then one next: line per failed build with a failed job.
func TreeText(o Out, t app.Tree, failedOnly bool) error {
	root := t.Root
	if failedOnly {
		root = pruneFailed(root)
	}
	showStages := !failedOnly || len(root.Children) > 0 || treeBad(root)
	tb := Table{Headers: []string{"", "", "", ""}}
	tb.Rows = append(tb.Rows, treeRow(o, root, ""))
	var next []string
	if root.Build.State == provider.StateFailed && len(root.Build.FailedJobs()) > 0 {
		next = append(next, root.Build.Key)
	}
	var add func(n *app.TreeNode, indent string, stages bool)
	add = func(n *app.TreeNode, indent string, stages bool) {
		type item struct {
			stage *provider.Stage
			node  *app.TreeNode
		}
		var items []item
		if stages && !n.NotRun && !n.Loop && !n.Superseded {
			for i := range n.Build.Stages {
				items = append(items, item{stage: &n.Build.Stages[i]})
			}
		}
		for _, k := range n.Children {
			items = append(items, item{node: k})
		}
		for i, it := range items {
			branch, more := "├─ ", "│  "
			if i == len(items)-1 {
				branch, more = "└─ ", "   "
			}
			if it.stage != nil {
				tb.Rows = append(tb.Rows, stageRow(o, n.Build, *it.stage, indent+branch))
				continue
			}
			tb.Rows = append(tb.Rows, treeRow(o, it.node, indent+branch))
			if it.node.Superseded {
				continue
			}
			if !it.node.NotRun && !it.node.Loop && it.node.Build.State == provider.StateFailed && len(it.node.Build.FailedJobs()) > 0 {
				next = append(next, it.node.Build.Key)
			}
			add(it.node, indent+more, true)
		}
	}
	add(root, "", showStages)
	if err := RenderRows(o, tb); err != nil {
		return err
	}
	for _, k := range next {
		if _, err := fmt.Fprintf(o.W, "next: bam logs %s --failed\n", k); err != nil {
			return err
		}
	}
	return nil
}

func treeRow(o Out, n *app.TreeNode, prefix string) []string {
	switch {
	case n.Superseded:
		return []string{prefix + style.Dim(o.Style, n.Build.Key), style.Dim(o.Style, style.Glyph(n.Build.State)+" "+style.Label(n.Build.State)),
			style.Dim(o.Style, Duration(n.Build.Duration)), style.Dim(o.Style, "earlier run")}
	case n.NotRun:
		return []string{prefix + n.PlanKey, style.Pending(o.Style) + " not run", "", ""}
	case n.Loop:
		return []string{prefix + o.Key(n.Build.Key, n.Build.URL), "↺ already shown above", "", ""}
	}
	return []string{prefix + o.Key(n.Build.Key, n.Build.URL), o.State(n.Build.State), Duration(elapsed(o, n.Build)), TreeNodeNote(n)}
}

// TreeNodeNote is what a build's tree line says after its duration: the
// stage whose job started it, for a build linked from a log, and its count
// of failed tests.
func TreeNodeNote(n *app.TreeNode) string {
	var parts []string
	if n.FromLog && n.Stage != "" {
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

// TreeLive redraws the tree in place on a TTY.
type TreeLive struct {
	o      Out
	failed bool
	lines  int
}

func NewTreeLive(o Out, failedOnly bool) *TreeLive { return &TreeLive{o: o, failed: failedOnly} }

func (l *TreeLive) Event(e app.TreeEvent) {
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
