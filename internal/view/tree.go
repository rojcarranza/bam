package view

import (
	"fmt"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// treeBad reports whether a node failed, was stopped or did not run.
func treeBad(n *app.TreeNode) bool {
	return n.NotRun || (n.Build.State.Finished() && n.Build.State != provider.StateSuccess)
}

// pruneFailed keeps the nodes on a path to a bad node; the root always stays.
func pruneFailed(n *app.TreeNode) *app.TreeNode {
	c := *n
	c.Children = nil
	for _, k := range n.Children {
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
		if stages && !n.NotRun && !n.Loop {
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
	case n.NotRun:
		return []string{prefix + n.PlanKey, style.Pending(o.Style) + " not run", "", ""}
	case n.Loop:
		return []string{prefix + o.Key(n.Build.Key, n.Build.URL), "↺ already shown above", "", ""}
	}
	extra := ""
	if c := len(n.Build.FailedTests); c > 0 {
		extra = fmt.Sprintf("%d tests failed", c)
	}
	return []string{prefix + o.Key(n.Build.Key, n.Build.URL), o.State(n.Build.State), Duration(elapsed(o, n.Build)), extra}
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
	d := TreeNodeDoc{PlanKey: n.PlanKey, Status: string(n.Build.State), Children: []TreeNodeDoc{}}
	switch {
	case n.NotRun:
		d.Status = "not_run"
		return d
	case n.Loop:
		d.Status = "loop"
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
