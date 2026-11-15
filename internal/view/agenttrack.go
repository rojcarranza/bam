package view

import (
	"fmt"

	"github.com/rojcarranza/bam/internal/app"
)

// AgentJobDoc is one job in the --json document of bam agent show.
type AgentJobDoc struct {
	Key        string `json:"key"`
	BuildKey   string `json:"build_key"`
	Name       string `json:"name"`
	State      string `json:"state"`
	DurationMS int64  `json:"duration_ms"`
}

// TallyDoc counts finished jobs and how many of them failed.
type TallyDoc struct {
	Jobs         int     `json:"jobs"`
	Failed       int     `json:"failed"`
	FailureShare float64 `json:"failure_share"`
}

// AgentTrackDoc is the --json document of bam agent show.
type AgentTrackDoc struct {
	AgentDoc
	BuildsScanned int           `json:"builds_scanned"`
	JobsScanned   int           `json:"jobs_scanned"`
	JobsUnknown   int           `json:"jobs_unknown"`
	Now           []AgentJobDoc `json:"now"`
	Recent        []AgentJobDoc `json:"recent"`
	This          TallyDoc      `json:"this_agent"`
	Others        TallyDoc      `json:"other_agents"`
	OtherAgents   int           `json:"other_agents_count"`
	Slow          bool          `json:"failing_more_than_others"`
}

func agentJobDoc(j app.AgentJob) AgentJobDoc {
	return AgentJobDoc{Key: j.Key, BuildKey: j.BuildKey, Name: j.Name, State: string(j.State), DurationMS: ms(j.Duration)}
}

func tallyDoc(t app.AgentTally) TallyDoc {
	return TallyDoc{Jobs: t.Jobs, Failed: t.Failed, FailureShare: t.Share()}
}

// AgentTrackJSON is the document of an agent's track record.
func AgentTrackJSON(r *app.AgentRecord) AgentTrackDoc {
	a := r.Agent
	return AgentTrackDoc{
		AgentDoc:      AgentDoc{ID: a.ID, Name: a.Name, Type: a.Type, Online: a.Online, Enabled: a.Enabled, Busy: a.Busy},
		BuildsScanned: r.Builds, JobsScanned: r.Jobs, JobsUnknown: r.Unknown,
		Now: Docs(r.Now, agentJobDoc), Recent: Docs(r.Recent, agentJobDoc),
		This: tallyDoc(r.This), Others: tallyDoc(r.Others), OtherAgents: r.OtherAgents, Slow: r.Slow,
	}
}

func tallyText(t app.AgentTally) string {
	if t.Jobs == 0 {
		return "no jobs"
	}
	return fmt.Sprintf("%.0f%% (%d of %d)", t.Share()*100, t.Failed, t.Jobs)
}

// AgentTrack prints what bam learned about one agent: its state, what it
// builds now, its latest jobs and how often they fail beside the others'.
func AgentTrack(o Out, r *app.AgentRecord) error {
	a := r.Agent
	state := "offline"
	switch {
	case !a.Enabled:
		state = "disabled"
	case a.Online && a.Busy:
		state = "building"
	case a.Online:
		state = "idle"
	}
	fmt.Fprintf(o.W, "%s  %s  %s\n", a.Name, a.Type, state)
	if len(r.Now) == 0 {
		field(o, "Now", "no running job in the newest builds")
	}
	for i, j := range r.Now {
		l := ""
		if i == 0 {
			l = "Now"
		}
		field(o, l, fmt.Sprintf("%s  %s", j.Key, j.Name))
	}
	field(o, "Failures", tallyText(r.This))
	if r.OtherAgents > 0 {
		who := "other agents"
		if r.OtherAgents == 1 {
			who = "other agent"
		}
		field(o, "Others", fmt.Sprintf("%s, %d %s", tallyText(r.Others), r.OtherAgents, who))
	}
	if r.Slow {
		field(o, "Warning", "its jobs fail at least twice as often as the other agents' jobs")
	}
	fmt.Fprintf(o.W, "\nLast %d jobs of %d recent builds:\n", len(r.Recent), r.Builds)
	if len(r.Recent) == 0 {
		fmt.Fprintln(o.W, "none finished on this agent")
	} else {
		t := Table{Headers: []string{"JOB", "BUILD", "STATE", "TIME"}}
		for _, j := range r.Recent {
			t.Rows = append(t.Rows, []string{j.Name, j.BuildKey, o.State(j.State), Duration(j.Duration)})
		}
		if err := t.Render(o); err != nil {
			return err
		}
	}
	if r.Unknown > 0 {
		fmt.Fprintf(o.W, "\n%d of %d jobs are left out: their logs do not name an agent\n", r.Unknown, r.Jobs)
	}
	return nil
}
