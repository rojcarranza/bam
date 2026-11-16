package view

import (
	"bytes"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view/style"
)

// BoardDoc is bam top --json.
type BoardDoc struct {
	GeneratedAt string          `json:"generated_at"`
	Server      BoardServerDoc  `json:"server"`
	Needs       []NeedDoc       `json:"needs_you"`
	Agents      *BoardAgentsDoc `json:"agents"`
	AgentsNote  string          `json:"agents_note,omitempty"`
	Queue       []QueueDoc      `json:"queue"`
	// QueueOthers counts the waiting builds the queue leaves out because
	// they wait for other agents, pipelines or projects.
	QueueOthers int             `json:"queue_others"`
	QueueNote   string          `json:"queue_note,omitempty"`
	Pins        []BoardGroupDoc `json:"pins"`
}

type BoardServerDoc struct {
	Alias   string `json:"alias"`
	URL     string `json:"url"`
	Version string `json:"version"`
}

type NeedDoc struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// BoardAgentsDoc counts the agents; Away lists those offline or disabled.
type BoardAgentsDoc struct {
	Total    int        `json:"total"`
	Online   int        `json:"online"`
	Busy     int        `json:"busy"`
	Offline  int        `json:"offline"`
	Disabled int        `json:"disabled"`
	Away     []AgentDoc `json:"away"`
	// Mine is how the agents you support are; absent when none are listed.
	Mine *BoardMineDoc `json:"mine,omitempty"`
}

type BoardGroupDoc struct {
	Name      string        `json:"name"`
	Pipelines []BoardPinDoc `json:"pipelines"`
}

type BoardPinDoc struct {
	Pin             string          `json:"pin"`
	Name            string          `json:"name,omitempty"`
	PlanKey         string          `json:"plan_key,omitempty"`
	Branch          string          `json:"branch,omitempty"`
	Results         []BoardPinBuild `json:"results"`
	Current         *BoardPinBuild  `json:"current"`
	Progress        *ProgressDoc    `json:"progress,omitempty"`
	LastDurationMS  int64           `json:"last_duration_ms"`
	UsualDurationMS int64           `json:"usual_duration_ms"`
	Error           string          `json:"error,omitempty"`
	// Branches are the plan branches that built in the last 7 days, newest
	// first; BranchesMore counts those left out.
	Branches     []BoardPinDoc `json:"branches"`
	BranchesMore int           `json:"branches_more"`
	// Tree is the pipelines the pin's build started, as bam build tree
	// --json gives them; only with bam top --tree.
	Tree *TreeDoc `json:"tree,omitempty"`
}

type BoardPinBuild struct {
	Key        string  `json:"key"`
	URL        string  `json:"url"`
	Number     int     `json:"number"`
	State      string  `json:"state"`
	StartedAt  *string `json:"started_at"`
	DurationMS int64   `json:"duration_ms"`
}

// BoardJSON is the document for b; url builds a page address.
func BoardJSON(b app.Board, url func(string) string) BoardDoc {
	d := BoardDoc{
		GeneratedAt: b.At.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Server:      BoardServerDoc{Alias: b.Server, URL: b.URL, Version: b.Version},
		Needs:       Docs(b.Needs, func(n app.Need) NeedDoc { return NeedDoc{Kind: n.Kind, Text: n.Text} }),
		AgentsNote:  b.AgentsNote,
		Queue:       BoardQueueJSON(b.Queue, url),
		QueueOthers: b.QueueOthers,
		QueueNote:   b.QueueNote,
		Pins:        []BoardGroupDoc{},
	}
	if a := b.Agents; a != nil {
		d.Agents = &BoardAgentsDoc{Total: a.Total, Online: a.Online, Busy: a.Busy, Offline: a.Offline, Disabled: a.Disabled, Away: AgentsJSON(a.Away)}
		if b.Mine != nil {
			d.Agents.Mine = mineDoc(b.Mine)
		}
	}
	for _, g := range b.Groups {
		gd := BoardGroupDoc{Name: g.Name, Pipelines: []BoardPinDoc{}}
		for _, p := range g.Pins {
			pd := pinDoc(p, url)
			pd.Branches = []BoardPinDoc{}
			for _, br := range p.Branches {
				pd.Branches = append(pd.Branches, pinDoc(br, url))
			}
			pd.BranchesMore = p.BranchesMore
			if p.HasChildren() {
				td := TreeJSON(*p.Tree, false)
				pd.Tree = &td
			}
			gd.Pipelines = append(gd.Pipelines, pd)
		}
		d.Pins = append(d.Pins, gd)
	}
	return d
}

func pinDoc(p app.PinView, url func(string) string) BoardPinDoc {
	pd := BoardPinDoc{Pin: p.Pin, Name: p.Name, PlanKey: p.PlanKey, Branch: p.Branch,
		Results:        Docs(p.Results, func(b provider.Build) BoardPinBuild { return pinBuildDoc(b, url) }),
		LastDurationMS: ms(p.Last), UsualDurationMS: ms(p.Usual), Progress: progressDoc(p.Progress)}
	if p.Current != nil {
		c := pinBuildDoc(*p.Current, url)
		pd.Current = &c
	}
	if p.Err != nil {
		pd.Error = p.Err.Error()
	}
	return pd
}

func pinBuildDoc(b provider.Build, url func(string) string) BoardPinBuild {
	return BoardPinBuild{Key: b.Key, URL: url(b.Key), Number: b.Number, State: string(b.State),
		StartedAt: timePtr(b.StartedAt), DurationMS: ms(b.Duration)}
}

// Board prints the board: the server, what needs you, the agents, the queue
// and the pinned pipelines by group.
func Board(o Out, b app.Board, url func(string) string) error {
	w := o.W
	head := b.Server
	if b.URL != "" {
		head += "  " + b.URL
	}
	if b.Version != "" {
		head += "  Bamboo " + b.Version
	}
	fmt.Fprintln(w, style.Bold(o.Style, head))

	section(o, "NEEDS YOU")
	if len(b.Needs) == 0 {
		fmt.Fprintln(w, "  Nothing needs you")
	}
	for _, n := range b.Needs {
		fmt.Fprintln(w, "  "+style.Paint(o.Style, "33", "!")+" "+n.Text)
	}

	if b.Mine != nil {
		section(o, "YOUR AGENTS")
		for _, l := range MineLines(b.Mine, minePaint(o)) {
			fmt.Fprintln(w, "  "+l)
		}
	} else if a := b.Agents; a == nil {
		section(o, "AGENTS")
		fmt.Fprintln(w, "  "+b.AgentsNote)
	} else {
		section(o, "AGENTS")
		fmt.Fprintf(w, "  %d agents: %s\n", a.Total, AgentSummaryText(a))
		for _, ag := range a.Away {
			why := "offline"
			if !ag.Enabled {
				why = "disabled"
			}
			fmt.Fprintf(w, "  %s  %s  %s\n", ag.Name, strings.ToLower(ag.Type), why)
		}
		if !b.MineSet {
			fmt.Fprintln(w, "  "+style.Dim(o.Style, "Show only your agents: bam agent mine add NAME-START"))
		}
	}

	section(o, queueTitle(b))
	switch {
	case !b.QueueOK:
		fmt.Fprintln(w, "  "+b.QueueNote)
	case len(b.Queue) == 0 && b.QueueOthers == 0:
		fmt.Fprintln(w, "  The queue is empty")
	default:
		if len(b.Queue) == 0 {
			fmt.Fprintln(w, "  Nothing waits for you")
		} else {
			t := queueTable(o, b.Queue, url, "  ")
			if err := t.Render(o); err != nil {
				return err
			}
		}
		if b.QueueOthers > 0 {
			fmt.Fprintln(w, "  "+style.Dim(o.Style, OthersText(b.QueueOthers)))
		}
	}

	section(o, "PINNED")
	if len(b.Groups) == 0 {
		fmt.Fprintln(w, "  No pipelines pinned. Pin one with: bam pin add GROUP PLAN")
		return nil
	}
	for _, g := range b.Groups {
		fmt.Fprintln(w, "  "+RuledLine(GroupTitle(g.Name), GroupState(g), ruleWidth(o),
			func(s string) string { return style.Bold(o.Style, s) },
			func(s string) string { return style.Dim(o.Style, s) },
			func(s string) string { return style.Dim(o.Style, s) }))
		t := Table{Headers: []string{"PIPELINE", "LAST 10", "NOW", "LAST", "USUAL"}, Flex: []int{0}, Indent: "    "}
		var failed []string
		for _, p := range g.Pins {
			if p.Err != nil {
				failed = append(failed, fmt.Sprintf("    %s could not be read: %s", p.Pin, p.Err))
				continue
			}
			t.Rows = append(t.Rows, pinRow(o, p, url, ""))
			for _, br := range p.Branches {
				t.Rows = append(t.Rows, pinRow(o, br, url, "  "))
			}
			if p.BranchesMore > 0 {
				t.Rows = append(t.Rows, []string{"  " + style.Dim(o.Style, fmt.Sprintf("+%d more branches", p.BranchesMore)), "", "", "", ""})
			}
		}
		if len(t.Rows) > 0 {
			if err := t.Render(o); err != nil {
				return err
			}
		}
		now := time.Now()
		if o.Now != nil {
			now = o.Now()
		}
		for _, p := range g.Pins {
			if p.Err != nil {
				continue
			}
			if rec := PassRecord(p.Results, now); rec != "" {
				line := p.Label() + " · " + rec
				if o.Width > 4 && utf8.RuneCountInString(line) > o.Width-4 {
					line = string([]rune(line)[:o.Width-5]) + "…"
				}
				fmt.Fprintln(w, "    "+style.Dim(o.Style, line))
			}
		}
		for _, f := range failed {
			fmt.Fprintln(w, f)
		}
		for _, p := range g.Pins {
			if err := pinTree(o, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// ruleWidth is the width of a group's heading: the output's, or 80 when it has
// none. The heading is indented by two columns, so it never exceeds Width-2.
func ruleWidth(o Out) int {
	if o.Width <= 0 {
		return 78
	}
	return min(max(o.Width-4, 20), o.Width-2)
}

// pinTree prints the pipelines a pin's build started, as bam build tree does,
// under the pin's name; nothing for a pin with none, or when bam read no tree.
func pinTree(o Out, p app.PinView) error {
	if !p.HasChildren() {
		return nil
	}
	fmt.Fprintln(o.W, "    "+style.Dim(o.Style, p.Label()+" · child pipelines"))
	var buf bytes.Buffer
	to := o
	to.W = &buf
	if to.Width > 4 {
		to.Width -= 4
	}
	if err := TreeText(to, *p.Tree, false); err != nil {
		return err
	}
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Fprintln(o.W, "    "+l)
	}
	return nil
}

func pinRow(o Out, p app.PinView, url func(string) string, indent string) []string {
	return []string{indent + o.Key(p.Label(), url(p.PlanKey)), results(o, p), now(o, p), Duration(p.Last), Duration(p.Usual)}
}

func section(o Out, title string) {
	fmt.Fprintln(o.W)
	fmt.Fprintln(o.W, style.Dim(o.Style, title))
}

// results is one glyph per finished build, the oldest first, so the newest
// is at the right.
func results(o Out, p app.PinView) string {
	if len(p.Results) == 0 {
		return "–"
	}
	var sb strings.Builder
	for i := len(p.Results) - 1; i >= 0; i-- {
		sb.WriteString(style.StateGlyph(o.Style, p.Results[i].State))
	}
	return sb.String()
}

// now is the build queued or running now, with its progress.
func now(o Out, p app.PinView) string {
	c := p.Current
	if c == nil {
		return "–"
	}
	s := fmt.Sprintf("#%d %s", c.Number, style.Label(c.State))
	if c.State == provider.StateRunning && p.Progress.Valid {
		s += fmt.Sprintf(" %.0f%%", p.Progress.Percent*100)
	}
	return s
}

// AgentSummaryText is the whole server's agents as four counts, the same
// words as the summary of your own agents.
func AgentSummaryText(a *app.AgentSummary) string {
	return fmt.Sprintf("%d building · %d idle · %d down · %d disabled", a.Busy, a.Online-a.Busy, a.Offline, a.Disabled)
}

// queueTitle names the queue section by what it lists.
func queueTitle(b app.Board) string {
	if b.QueueFiltered {
		return "YOUR QUEUE"
	}
	return "QUEUE"
}
