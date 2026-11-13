package view

import (
	"fmt"
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
		WaitedMS: ms(r.Waited), WaitedAtLeast: r.AtLeast}
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
	t := Table{Headers: []string{"#", "BUILD", "REASON", "WAITED"}, Flex: []int{2}}
	for _, r := range q {
		t.Rows = append(t.Rows, []string{fmt.Sprint(r.Position), o.Key(r.Key, url(r.Key)), r.Reason, waitedText(r)})
	}
	return t.Render(o)
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
