package view

import (
	"fmt"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
)

// QueueText says where a queued build waits, "queue 3 of 7"; empty when the
// build is not queued or its place is not known.
func QueueText(b provider.Build) string {
	if b.State != provider.StateQueued || b.QueuePosition == 0 {
		return ""
	}
	return fmt.Sprintf("queue %d of %d", b.QueuePosition, b.QueueLength)
}

// WaitText is what a queued build shows beside its state: its place in the
// queue and why it waits, "queue 3 of 7 · no agent can run it". Either part
// is left out when bam does not know it.
func WaitText(b provider.Build) string {
	var parts []string
	if q := QueueText(b); q != "" {
		parts = append(parts, q)
	}
	if b.State == provider.StateQueued {
		if w := WhyText(b.Why); w != "" {
			parts = append(parts, w)
		}
	}
	return strings.Join(parts, " · ")
}

// WhyText says why a build waits, or "" when bam could not tell. The agents
// are named when three or fewer can run the job.
func WhyText(w *provider.Why) string {
	if w == nil {
		return ""
	}
	var who string
	switch {
	case w.Count == 1:
		who = "1 agent can run it"
	case w.Count > 1:
		who = fmt.Sprintf("%d agents can run it", w.Count)
	}
	if who != "" && len(w.Agents) > 0 {
		who += " (" + strings.Join(w.Agents, ", ") + ")"
	}
	switch w.Case {
	case provider.WhyNoAgent:
		return "no agent can run it"
	case provider.WhyBusy:
		switch {
		case w.Count == 1:
			return who + ", busy, offline or disabled"
		case w.Count > 1:
			return who + ", all busy, offline or disabled"
		}
		return "every agent is busy, offline or disabled"
	case provider.WhyNext:
		return "agents are free, it is next"
	case provider.WhyAhead:
		return fmt.Sprintf("agents are free, %d before it", w.Ahead)
	}
	return who
}

// WhyDoc is the why-it-waits part of a queued build's JSON. Every field is
// absent when bam could not tell.
type WhyDoc struct {
	Why      string   `json:"why,omitempty"`
	WhyCase  string   `json:"why_case,omitempty"`
	AnyAgent *bool    `json:"any_agent_can_run,omitempty"`
	Agents   []string `json:"agents_that_can_run,omitempty"`
}

func whyDoc(w *provider.Why) WhyDoc {
	if w == nil {
		return WhyDoc{}
	}
	return WhyDoc{Why: WhyText(w), WhyCase: w.Case, AnyAgent: w.AnyAgent, Agents: w.Agents}
}

// QueueDoc is one queued build in bam queue --json.
type QueueDoc struct {
	Position int    `json:"position"`
	Key      string `json:"key"`
	URL      string `json:"url"`
	PlanKey  string `json:"plan_key"`
	Number   int    `json:"number"`
	Reason   string `json:"reason"`
	// WaitedMS is how long the build has waited; WaitedAtLeast is true when
	// the build's record gave no start and bam counted from when it saw it.
	WaitedMS      int64 `json:"waited_ms"`
	WaitedAtLeast bool  `json:"waited_at_least"`
	WhyDoc
}

// AgentDoc is one build agent in bam agent list --json.
type AgentDoc struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Online  bool   `json:"online"`
	Enabled bool   `json:"enabled"`
	Busy    bool   `json:"busy"`
}

func QueueJSON(q []app.QueueRow, url func(string) string) []QueueDoc {
	return Docs(q, func(r app.QueueRow) QueueDoc { return queueDoc(r, url) })
}

func queueDoc(r app.QueueRow, url func(string) string) QueueDoc {
	return QueueDoc{Position: r.Position, Key: r.Key, URL: url(r.Key), PlanKey: r.PlanKey, Number: r.Number, Reason: r.Reason,
		WaitedMS: ms(r.Waited), WaitedAtLeast: r.AtLeast, WhyDoc: whyDoc(r.Why)}
}

// waitedText is the time a queued build has waited, "at least" when bam
// could only count from when it first saw the build.
func waitedText(r app.QueueRow) string {
	d := "0s"
	if r.Waited >= time.Second {
		d = Duration(r.Waited)
	}
	if r.AtLeast {
		return "at least " + d
	}
	return d
}

func AgentsJSON(as []provider.Agent) []AgentDoc {
	return Docs(as, func(a provider.Agent) AgentDoc {
		return AgentDoc{ID: a.ID, Name: a.Name, Type: a.Type, Online: a.Online, Enabled: a.Enabled, Busy: a.Busy}
	})
}

// QueueList prints the queued builds, first in line first.
func QueueList(o Out, q []app.QueueRow, url func(string) string) error {
	if len(q) == 0 {
		fmt.Fprintln(o.W, "The queue is empty")
		return nil
	}
	t := queueTable(o, q, url, "")
	return t.Render(o)
}

// queueTable lays out the queue. The WHY column is there when bam knows the
// reason of at least one build, so a server that tells nothing keeps the
// four columns.
func queueTable(o Out, q []app.QueueRow, url func(string) string, indent string) Table {
	t := Table{Headers: []string{"#", "BUILD", "REASON", "WAITED"}, Flex: []int{2}, Indent: indent}
	why := false
	for _, r := range q {
		why = why || WhyText(r.Why) != ""
	}
	if why {
		t.Headers = append(t.Headers, "WHY")
	}
	for _, r := range q {
		row := []string{fmt.Sprint(r.Position), o.Key(r.Key, url(r.Key)), r.Reason, waitedText(r)}
		if why {
			row = append(row, WhyText(r.Why))
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

// AgentList prints the build agents with whether each is online, enabled
// and busy, which is what says why a queued build is not starting.
func AgentList(o Out, as []provider.Agent) error {
	if len(as) == 0 {
		fmt.Fprintln(o.W, "No build agents")
		return nil
	}
	t := Table{Headers: []string{"AGENT", "TYPE", "ONLINE", "ENABLED", "BUSY"}}
	for _, a := range as {
		t.Rows = append(t.Rows, []string{a.Name, a.Type, yesNo(a.Online), yesNo(a.Enabled), yesNo(a.Busy)})
	}
	return t.Render(o)
}
