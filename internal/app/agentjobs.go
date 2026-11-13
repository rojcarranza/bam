package app

import (
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/atomicfile"
)

// agentJobsTTL is how long bam keeps the jobs an agent can run. They change
// only when someone edits an agent's capabilities or a job's requirements,
// and Bamboo tells them one agent at a time, so on a server with hundreds of
// agents asking every poll would read hundreds of agent details every poll.
const agentJobsTTL = time.Hour

// agentJobsRetry is how long bam waits before it asks for agent details again
// after a read that failed, so a struggling server is not asked every poll.
const agentJobsRetry = 5 * time.Minute

// agentJobs is what one agent's details said: the jobs it can run, sorted,
// and when bam read them.
type agentJobs struct {
	Read time.Time
	Jobs []string
}

// canRun reports whether the agent's list names the job planKey.
func (a agentJobs) canRun(planKey string) bool {
	_, ok := slices.BinarySearch(a.Jobs, planKey)
	return ok
}

// runsPlan reports whether the agent's list names a job of the plan planKey,
// whose jobs are keyed PLAN-JOB1.
func (a agentJobs) runsPlan(planKey string) bool {
	prefix := planKey + "-"
	i, _ := slices.BinarySearch(a.Jobs, prefix)
	return i < len(a.Jobs) && strings.HasPrefix(a.Jobs[i], prefix)
}

// AgentJobStore keeps the jobs each agent can run on disk, one file per
// server origin, so bam queue, a watched build and the board share one read
// of every agent an hour. It is a cache: a missing, corrupt or old file is
// read again from the server, never an error.
type AgentJobStore struct {
	Dir string
}

// agentJobsFileVersion is the format of an agent jobs file; a file of
// another version is dropped.
const agentJobsFileVersion = 1

// agentJobsFile holds each distinct job list once: the agents of one pool
// usually run the same jobs, so the file stays small on a large server.
type agentJobsFile struct {
	Version int                      `json:"version"`
	Lists   [][]string               `json:"lists"`
	Agents  map[string]agentJobsSeen `json:"agents"` // by agent name
}

type agentJobsSeen struct {
	Read time.Time `json:"read"`
	List int       `json:"list"` // index into Lists
}

func (s *AgentJobStore) path(origin string) string {
	return filepath.Join(s.Dir, url.QueryEscape(origin)+".json")
}

// Load returns what origin's agents said within agentJobsTTL of now, by
// agent name. It never fails: a file it cannot use gives an empty map.
func (s *AgentJobStore) Load(origin string, now time.Time) map[string]agentJobs {
	out := map[string]agentJobs{}
	data, err := os.ReadFile(s.path(origin))
	if err != nil {
		return out
	}
	var f agentJobsFile
	if json.Unmarshal(data, &f) != nil || f.Version != agentJobsFileVersion {
		return out
	}
	for name, seen := range f.Agents {
		if seen.List < 0 || seen.List >= len(f.Lists) || now.Sub(seen.Read) >= agentJobsTTL || seen.Read.After(now) {
			continue
		}
		jobs := slices.Clone(f.Lists[seen.List])
		slices.Sort(jobs) // a file written by hand or by an older bam may not be sorted
		out[name] = agentJobs{Read: seen.Read, Jobs: jobs}
	}
	return out
}

// Save writes what origin's agents said. A write error is dropped; the
// cache is only lost.
func (s *AgentJobStore) Save(origin string, agents map[string]agentJobs) {
	f := agentJobsFile{Version: agentJobsFileVersion, Lists: [][]string{}, Agents: make(map[string]agentJobsSeen, len(agents))}
	index := map[string]int{}
	for _, name := range slices.Sorted(maps.Keys(agents)) {
		a := agents[name]
		key := strings.Join(a.Jobs, "\n")
		i, ok := index[key]
		if !ok {
			i = len(f.Lists)
			index[key] = i
			f.Lists = append(f.Lists, append([]string{}, a.Jobs...))
		}
		f.Agents[name] = agentJobsSeen{Read: a.Read, List: i}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	_ = atomicfile.Write(s.path(origin), data, 0o600) // the cache is best effort
}
