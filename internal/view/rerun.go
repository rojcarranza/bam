package view

import (
	"fmt"

	"github.com/r0jjames/bam-cli/internal/app"
)

// RerunList is the question bam build tree --rerun-failed asks: what it
// will restart, with the first failed stage and job of each.
func RerunList(o Out, cands []*app.TreeNode) error {
	noun := "builds"
	if len(cands) == 1 {
		noun = "build"
	}
	if _, err := fmt.Fprintf(o.W, "Rerun the failed jobs of %d %s?\n", len(cands), noun); err != nil {
		return err
	}
	tb := Table{Indent: "  ", Headers: []string{"", "", ""}}
	for _, n := range cands {
		where := ""
		for _, s := range n.Build.Stages {
			for _, j := range s.Jobs {
				if where == "" && j.State == n.Build.State {
					where = s.Name + " › " + j.Name
				}
			}
		}
		tb.Rows = append(tb.Rows, []string{o.Key(n.Build.Key, n.Build.URL), o.State(n.Build.State), where})
	}
	return RenderRows(o, tb)
}
