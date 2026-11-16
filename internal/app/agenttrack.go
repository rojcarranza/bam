package app

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/atomicfile"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// DefaultTrackBuilds is how many of the server's newest builds bam looks
// through for an agent's track record.
const DefaultTrackBuilds = 40

// TrackJobs is how many of an agent's jobs the track record lists.
const TrackJobs = 20

// jobAgentKeep is how many finished jobs the job agent store remembers.
const jobAgentKeep = 20000

// jobAgentReaders is how many job logs bam reads at once.
const jobAgentReaders = 4

// JobAgentStore remembers which agent ran each finished job, one file per
// server origin. A finished job's agent never changes, so a job is read once.
// It is a cache: a missing or corrupt file is read again, never an error.
type JobAgentStore struct {
	Dir string
	mu  sync.Mutex
}

type jobAgentEntry struct {
	Job   string `json:"job"`
	Agent string `json:"agent"`
}

type jobAgentFile struct {
	Version int             `json:"version"`
	Jobs    []jobAgentEntry `json:"jobs"` // oldest first
}

func (s *JobAgentStore) path(origin string) string {
	return filepath.Join(s.Dir, url.QueryEscape(origin)+".json")
}

func (s *JobAgentStore) load(origin string) []jobAgentEntry {
	data, err := os.ReadFile(s.path(origin))
	if err != nil {
		return nil
	}
	var f jobAgentFile
	if json.Unmarshal(data, &f) != nil || f.Version != 2 {
		return nil
	}
	return f.Jobs
}

// Load returns the agent of each job bam remembers, by job result key.
func (s *JobAgentStore) Load(origin string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, e := range s.load(origin) {
		out[e.Job] = e.Agent
	}
	return out
}

// Add remembers new jobs, keeping the newest jobAgentKeep. A write error is
// dropped; the cache is only lost.
func (s *JobAgentStore) Add(origin string, jobs map[string]string) {
	if len(jobs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	have := s.load(origin)
	known := map[string]bool{}
	for _, e := range have {
		known[e.Job] = true
	}
	for _, k := range sortedKeys(jobs) {
		if !known[k] {
			have = append(have, jobAgentEntry{Job: k, Agent: jobs[k]})
		}
	}
	if len(have) > jobAgentKeep {
		have = have[len(have)-jobAgentKeep:]
	}
	data, err := json.Marshal(jobAgentFile{Version: 2, Jobs: have})
	if err != nil {
		return
	}
	_ = atomicfile.Write(s.path(origin), data, 0o600)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// jobAgents finds out which agent ran each job in keys, from what bam
// remembers and, for the rest, from the jobs' logs. A job whose log names no
// agent maps to "". finished says which jobs are over, so only they are
// remembered. err is the first log read that failed, which leaves that job out
// of the answer.
func (s *Service) jobAgents(ctx context.Context, keys []string, finished map[string]bool) (map[string]string, error) {
	r, ok := s.P.(provider.JobAgentReader)
	if !ok {
		return nil, errs.Bamboof("this server cannot say which agent ran a job").WithWhy("it does not give a job's log to read").Wrap(errs.ErrUnsupported)
	}
	out := map[string]string{}
	var known map[string]string
	if s.JobAgents != nil {
		known = s.JobAgents.Load(s.Origin)
	}
	var todo []string
	for _, k := range keys {
		if a, ok := known[k]; ok {
			out[k] = a
		} else {
			todo = append(todo, k)
		}
	}
	var (
		mu    sync.Mutex
		first error
		wg    sync.WaitGroup
		sem   = make(chan struct{}, jobAgentReaders)
	)
	learned := map[string]string{}
	for _, k := range todo {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			a, err := r.JobAgent(ctx, k)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if first == nil {
					first = err
				}
				return
			}
			out[k] = a
			if finished[k] && a != "" {
				learned[k] = a
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.JobAgents != nil {
		s.JobAgents.Add(s.Origin, learned)
	}
	return out, first
}

// FillJobAgents sets Agent on each job of b that has left the queue, and on b
// itself when its jobs ran on one agent or several. A server that cannot say,
// or a log that names no agent, leaves the field empty; it never fails.
func (s *Service) FillJobAgents(ctx context.Context, b *provider.Build) {
	var keys []string
	finished := map[string]bool{}
	for _, st := range b.Stages {
		for _, j := range st.Jobs {
			if jobStarted(j.State) {
				keys = append(keys, j.Key)
				finished[j.Key] = jobOver(j.State)
			}
		}
	}
	agents, _ := s.jobAgents(ctx, keys, finished)
	var names []string
	for i := range b.Stages {
		for k := range b.Stages[i].Jobs {
			j := &b.Stages[i].Jobs[k]
			if a := agents[j.Key]; a != "" {
				j.Agent = a
				if !slices.Contains(names, a) {
					names = append(names, a)
				}
			}
		}
	}
	if b.Agent == "" {
		b.Agent = strings.Join(names, ", ")
	}
}

func jobStarted(st provider.State) bool {
	return st != provider.StateQueued && st != provider.StateNotBuilt
}

func jobOver(st provider.State) bool {
	return st == provider.StateSuccess || st == provider.StateFailed || st == provider.StateStopped
}

// AgentJob is one job an agent ran.
type AgentJob struct {
	Key      string
	BuildKey string
	Name     string
	State    provider.State
	Duration time.Duration
}

// AgentTally counts the finished jobs of one or more agents.
type AgentTally struct {
	Jobs, Failed int
}

// Share is the part of the jobs that failed, 0 to 1; 0 when there are none.
func (t AgentTally) Share() float64 {
	if t.Jobs == 0 {
		return 0
	}
	return float64(t.Failed) / float64(t.Jobs)
}

// AgentRecord is what bam learned about one agent from the newest builds.
type AgentRecord struct {
	Agent provider.Agent
	// Builds is how many builds were read, Jobs how many of their jobs
	// started, and Unknown how many of those no log named an agent for or
	// whose log could not be read.
	Builds, Jobs, Unknown int
	Now                   []AgentJob // jobs the agent is running now
	Recent                []AgentJob // its latest finished jobs, newest first, at most TrackJobs
	This                  AgentTally // all its finished jobs in the builds
	Others                AgentTally // every other agent's finished jobs in the builds
	OtherAgents           int        // how many other agents ran one
	// Slow is true when This.Share is at least twice Others.Share, the agent
	// failed at least slowMinJobs jobs and the other agents finished at least
	// slowMinJobs, so the comparison means something.
	Slow bool
}

// slowMinJobs is how many jobs an agent needs, and how many it must have
// failed, before it is called slow to the rest.
const slowMinJobs = 5

// AgentRecord reads the track record of the agent called name from the
// server's newest builds: it lists them, reads each one's jobs and finds each
// job's agent. The server must list agents and give job logs.
func (s *Service) AgentRecord(ctx context.Context, name string, builds int) (*AgentRecord, error) {
	agent, err := s.FindAgent(ctx, name)
	if err != nil {
		return nil, err
	}
	rb, ok := s.P.(provider.RecentBuilder)
	if !ok {
		return nil, errs.Bamboof("this server cannot list its recent builds").Wrap(errs.ErrUnsupported)
	}
	if builds <= 0 {
		builds = DefaultTrackBuilds
	}
	recent, err := rb.RecentBuilds(ctx, builds)
	if err != nil {
		return nil, err
	}
	full := make([]*provider.Build, len(recent))
	var wg sync.WaitGroup
	sem := make(chan struct{}, jobAgentReaders)
	for i, b := range recent {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if got, err := s.P.GetBuild(ctx, b.Key); err == nil {
				full[i] = &got
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var keys []string
	finished := map[string]bool{}
	for _, b := range full {
		if b == nil {
			continue
		}
		for _, st := range b.Stages {
			for _, j := range st.Jobs {
				if jobStarted(j.State) {
					keys = append(keys, j.Key)
					finished[j.Key] = jobOver(j.State)
				}
			}
		}
	}
	agents, readErr := s.jobAgents(ctx, keys, finished)
	if readErr != nil && len(agents) == 0 && len(keys) > 0 {
		return nil, readErr
	}
	rec := &AgentRecord{Agent: agent}
	mine := AgentBaseName(agent.Name)
	others := map[string]bool{}
	for _, b := range full {
		if b == nil {
			continue
		}
		rec.Builds++
		for _, st := range b.Stages {
			for _, j := range st.Jobs {
				if !jobStarted(j.State) {
					continue
				}
				rec.Jobs++
				who, read := agents[j.Key]
				if !read || who == "" {
					rec.Unknown++
					continue
				}
				done := jobOver(j.State)
				if AgentBaseName(who) != mine {
					if done {
						rec.Others.Jobs++
						if j.State == provider.StateFailed {
							rec.Others.Failed++
						}
						others[AgentBaseName(who)] = true
					}
					continue
				}
				aj := AgentJob{Key: j.Key, BuildKey: b.Key, Name: st.Name + " / " + j.Name, State: j.State, Duration: j.Duration}
				if !done {
					rec.Now = append(rec.Now, aj)
					continue
				}
				rec.This.Jobs++
				if j.State == provider.StateFailed {
					rec.This.Failed++
				}
				if len(rec.Recent) < TrackJobs {
					rec.Recent = append(rec.Recent, aj)
				}
			}
		}
	}
	rec.addRunningNow(s.agentNow(ctx, agent, rec))
	rec.OtherAgents = len(others)
	rec.Slow = rec.This.Failed >= slowMinJobs && rec.Others.Jobs >= slowMinJobs && rec.This.Share() >= 2*rec.Others.Share() && rec.This.Share() > 0
	return rec, nil
}

// agentNow is the job the agent runs now when the newest builds did not show
// it, such as a build on a plan branch, from the lookup bam top uses. nil when
// the agent is not busy, runs a job already listed, or bam found no build.
func (s *Service) agentNow(ctx context.Context, agent provider.Agent, rec *AgentRecord) *AgentJob {
	if !agent.Busy || !agent.Online || len(rec.Now) > 0 {
		return nil
	}
	work := s.LookAgentWork(ctx, []provider.Agent{agent}, nil)
	b, ok := work.BuildOf(agent.Name)
	if !ok {
		return nil
	}
	return &AgentJob{Key: b.Job, BuildKey: b.BuildKey, Name: b.Label(), State: provider.StateRunning}
}

func (r *AgentRecord) addRunningNow(j *AgentJob) {
	if j != nil {
		r.Now = append(r.Now, *j)
	}
}
