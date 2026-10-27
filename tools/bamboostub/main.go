// Command bamboostub serves a fake Bamboo Data Center REST API from the
// fixtures in internal/provider/bamboo/testdata, so bam can be driven by hand
// without a Bamboo server, a token or a network.
//
//	make stub
//	go run ./tools/bamboostub -addr 127.0.0.1:7990
//
// Then, in another terminal:
//
//	BAM_URL=http://127.0.0.1:7990 BAM_TOKEN=devtoken bam
//
// Reads are the fixtures, retargeted to whatever key was asked for. A
// triggered build is simulated: it stays queued, then runs, then succeeds,
// and bam build cancel stops it. With -child, its end queues a build of the
// child plan, as Bamboo does, so bam build tree and bam run --watch have a
// tree to follow. Any bearer token is accepted.
//
// Flags:
//
//	-addr HOST:PORT   listen address (default 127.0.0.1:7990)
//	-fixtures DIR     fixture directory
//	-queued DURATION  how long a triggered build stays queued (default 5s)
//	-running DURATION how long it then runs (default 10s)
//	-child PLAN       plan a triggered build queues a child build of when it ends
//	-child-fails      that child build fails
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const api = "/rest/api/latest"

// The keys the fixtures were recorded under. A request for another key gets
// the same body with these replaced, so every plan has builds and logs.
const (
	fixturePlan   = "PROJ-BUILD"
	fixtureBuild  = "PROJ-BUILD12-44"
	fixtureBranch = "PROJ-BUILD12" // the branch plan fixtureBuild ran on
	fixtureNum    = "44"
	fixtureJob    = "PROJ-BUILD12-INT-44"
	fixtureResult = "PROJ-BUILD-482"
	fixtureProj   = "PROJ"
)

type stub struct {
	fixtures   string
	queued     time.Duration
	running    time.Duration
	child      string // plan of the build a triggered build queues when it ends; "" for none
	childFails bool
	now        func() time.Time

	mu    sync.Mutex
	live  map[string]*build
	lastN int
}

// build is one simulated build: what was triggered in this process, as
// opposed to the finished builds the fixtures describe.
type build struct {
	planKey string
	number  int
	started time.Time // a child build does not exist before it
	stopped time.Time // when bam build cancel stopped it; zero while it runs on
	parent  string    // the build whose end queued this one
	fails   bool
}

// visible reports whether b exists yet: a child is queued when its parent
// ends, not when the parent is triggered.
func (s *stub) visible(b *build) bool { return !s.now().Before(b.started) }

func newStub(fixtures string) *stub {
	return &stub{
		fixtures: fixtures,
		queued:   5 * time.Second,
		running:  10 * time.Second,
		now:      time.Now,
		live:     map[string]*build{},
		lastN:    900,
	}
}

// phase reports a simulated build's lifeCycleState and state. A stopped build
// is Finished/Unknown, which is what Bamboo reports and bam shows as stopped.
func (s *stub) phase(b *build) (string, string) {
	switch d := s.now().Sub(b.started); {
	case !b.stopped.IsZero():
		return "Finished", "Unknown"
	case d < s.queued:
		return "Queued", "Unknown"
	case d < s.queued+s.running:
		return "InProgress", "Unknown"
	case b.fails:
		return "Finished", "Failed"
	default:
		return "Finished", "Successful"
	}
}

// ended is when a simulated build's clock stopped: when it was cancelled, when
// it finished, or now while it is still going, so a finished build keeps the
// duration and completed time it finished with.
func (s *stub) ended(b *build) time.Time {
	end := b.started.Add(s.queued + s.running)
	if !b.stopped.IsZero() && b.stopped.Before(end) {
		end = b.stopped
	}
	if now := s.now(); now.Before(end) {
		return now
	}
	return end
}

func (s *stub) fixture(w http.ResponseWriter, name string) []byte {
	b, err := os.ReadFile(filepath.Join(s.fixtures, name))
	if err != nil {
		log.Printf("fixture %s: %v", name, err)
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return nil
	}
	return b
}

// serve writes a fixture with its recorded key replaced by the one asked for.
func (s *stub) serve(w http.ResponseWriter, name, from, to string) {
	body := s.fixture(w, name)
	if body == nil {
		return
	}
	if from != to {
		body = []byte(strings.ReplaceAll(string(body), from, to))
	}
	writeJSON(w, body)
}

func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// buildKeyRe tells a build or job result key from a plan or branch key: only
// the former ends in a build number.
var buildKeyRe = regexp.MustCompile(`-\d+$`)

// jobKeyRe matches a job result key, PROJ-PLAN-JOB-44: one part more than a
// build key.
var jobKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-\d+$`)

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// liveDetail renders a simulated build the way Bamboo's result detail reads,
// down to the one stage and one job bam needs to draw its panels.
func (s *stub) liveDetail(key string, b *build) []byte {
	out, err := json.Marshal(s.liveDoc(key, b))
	if err != nil { // the document is built here, so this cannot happen
		log.Printf("live detail %s: %v", key, err)
	}
	return out
}

func (s *stub) liveDoc(key string, b *build) map[string]any {
	life, state := s.phase(b)
	reason := "Manual build by J Doe"
	if b.parent != "" {
		reason = fmt.Sprintf(`Child of <a href="/browse/%s">%s</a>`, b.parent, b.parent)
	}
	elapsed := int(s.ended(b).Sub(b.started) / time.Millisecond)
	doc := map[string]any{
		"key": key, "buildResultKey": key, "buildNumber": b.number,
		"state": state, "lifeCycleState": life,
		"buildStartedTime": stamp(b.started),
		"buildDuration":    elapsed,
		"buildReason":      reason,
		"plan":             map[string]any{"key": b.planKey, "shortName": "Build and test"},
		"stages": map[string]any{"size": 1, "stage": []any{map[string]any{
			"name": "Build", "state": state, "lifeCycleState": life,
			"results": map[string]any{"size": 1, "result": []any{map[string]any{
				"buildResultKey": fmt.Sprintf("%s-JOB1-%d", b.planKey, b.number),
				"state":          state, "lifeCycleState": life,
				"buildDuration": elapsed,
				"plan":          map[string]any{"key": b.planKey + "-JOB1", "shortName": "Build"},
			}}},
		}}},
	}
	if life == "Finished" {
		doc["buildCompletedTime"] = stamp(s.ended(b))
	}
	return doc
}

// serveList answers a plan's build list: the fixture's builds, retargeted,
// after the simulated builds of that plan that exist yet, newest first.
func (s *stub) serveList(w http.ResponseWriter, planKey string) {
	body := s.fixture(w, "results.json")
	if body == nil {
		return
	}
	body = []byte(strings.ReplaceAll(string(body), fixturePlan, planKey))
	var doc struct {
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		log.Printf("results.json: %v", err)
		http.Error(w, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	var live []any
	s.mu.Lock()
	for n := s.lastN; n > 900; n-- {
		key := fmt.Sprintf("%s-%d", planKey, n)
		if b := s.live[key]; b != nil && s.visible(b) {
			live = append(live, s.liveDoc(key, b))
		}
	}
	s.mu.Unlock()
	fixed, _ := doc.Results["result"].([]any)
	doc.Results["result"] = append(live, fixed...)
	doc.Results["size"] = len(live) + len(fixed)
	out, err := json.Marshal(doc)
	if err != nil { // decoded from JSON a moment ago, so this cannot happen
		log.Printf("results for %s: %v", planKey, err)
	}
	writeJSON(w, out)
}

func (s *stub) handleResult(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, api+"/result/")
	expand := r.URL.Query().Get("expand")
	if !buildKeyRe.MatchString(key) { // a plan or branch key: list its builds
		s.serveList(w, key)
		return
	}
	switch {
	case strings.Contains(expand, "logEntries"):
		s.serve(w, "log_entries.json", fixtureJob, key)
		return
	case strings.Contains(expand, "testResults"):
		// As Bamboo 12 does: a build's result carries no tests, each
		// job's result carries its own.
		if jobKeyRe.MatchString(key) {
			s.serve(w, "job_failed_tests.json", fixtureJob, key)
		} else {
			writeJSON(w, []byte(fmt.Sprintf(`{"key":%q,"buildResultKey":%q}`, key, key)))
		}
		return
	case expand == "variables":
		s.serve(w, "result_variables.json", fixtureResult, key)
		return
	}
	s.mu.Lock()
	b := s.live[key]
	s.mu.Unlock()
	if b != nil {
		if !s.visible(b) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, s.liveDetail(key, b))
		return
	}
	if jobKeyRe.MatchString(key) {
		s.serveJobDetail(w, key)
		return
	}
	s.serveDetail(w, key)
}

// serveDetail writes the recorded build detail as build key: its number, plan
// and job keys become the asked-for ones, and only the recorded branch plan
// keeps its branch, so PROJ-BUILD-482 reads as build #482 of PROJ-BUILD.
func (s *stub) serveDetail(w http.ResponseWriter, key string) {
	if text, ok := s.detail(w, key); ok {
		writeJSON(w, []byte(text))
	}
}

// serveJobDetail writes a job result as its build's detail lists it, or 404
// when the build has no such job.
func (s *stub) serveJobDetail(w http.ResponseWriter, key string) {
	i := strings.LastIndex(key, "-")
	j := strings.LastIndex(key[:i], "-")
	text, ok := s.detail(w, key[:j]+key[i:])
	if !ok {
		return
	}
	var doc struct {
		Stages struct {
			Stage []struct {
				Results struct {
					Result []map[string]any `json:"result"`
				} `json:"results"`
			} `json:"stage"`
		} `json:"stages"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		log.Printf("job detail %s: %v", key, err)
		http.Error(w, "fixture unreadable", http.StatusInternalServerError)
		return
	}
	for _, st := range doc.Stages.Stage {
		for _, job := range st.Results.Result {
			if job["buildResultKey"] == key {
				job["key"] = key
				job["buildNumber"], _ = strconv.Atoi(key[i+1:])
				out, err := json.Marshal(job)
				if err != nil { // decoded from JSON just above, so this cannot happen
					log.Printf("job detail %s: %v", key, err)
				}
				writeJSON(w, out)
				return
			}
		}
	}
	http.Error(w, "no such job result", http.StatusNotFound)
}

// detail is the recorded build detail as build key, or false after writing
// the error.
func (s *stub) detail(w http.ResponseWriter, key string) (string, bool) {
	body := s.fixture(w, "result_detail.json")
	if body == nil {
		return "", false
	}
	i := strings.LastIndex(key, "-")
	plan, num := key[:i], key[i+1:]
	text := strings.NewReplacer(
		fixtureBuild, key,
		fixtureBranch+"-", plan+"-",
		"-"+fixtureNum+`"`, "-"+num+`"`,
		`"buildNumber": `+fixtureNum, `"buildNumber": `+num,
	).Replace(string(body))
	if plan != fixtureBranch {
		var doc map[string]any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			log.Printf("result detail %s: %v", key, err)
			http.Error(w, "fixture unreadable", http.StatusInternalServerError)
			return "", false
		}
		doc["plan"] = map[string]any{"key": plan, "shortName": "Build and test"}
		doc["planName"] = "Build and test"
		out, err := json.Marshal(doc)
		if err != nil { // decoded from JSON just above, so this cannot happen
			log.Printf("result detail %s: %v", key, err)
		}
		text = string(out)
	}
	return text, true
}

// handleStatus answers the progress endpoint: a running simulated build
// reports its share of the configured run time, and everything else reports
// as finished, which is what Bamboo does.
func (s *stub) handleStatus(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, api+"/result/status/")
	s.mu.Lock()
	b := s.live[key]
	s.mu.Unlock()
	if b == nil {
		writeJSON(w, []byte(`{"currentStage":"","finished":true}`))
		return
	}
	if life, _ := s.phase(b); life != "InProgress" {
		writeJSON(w, []byte(`{"currentStage":"","finished":true}`))
		return
	}
	elapsed := s.now().Sub(b.started) - s.queued
	pct := float64(elapsed) / float64(s.running)
	writeJSON(w, fmt.Appendf(nil,
		`{"currentStage":"Build","finished":false,"progress":{"isValid":true,"averageBuildDuration":%d,"buildTime":%d,"percentageCompleted":%.2f}}`,
		s.running.Milliseconds(), elapsed.Milliseconds(), pct))
}

func (s *stub) handleQueue(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, api+"/queue/")
	switch r.Method {
	case http.MethodPost:
		_ = r.ParseForm()
		s.mu.Lock()
		s.lastN++
		num := s.lastN
		resultKey := fmt.Sprintf("%s-%d", key, num)
		s.live[resultKey] = &build{planKey: key, number: num, started: s.now()}
		if s.child != "" && key != s.child {
			s.lastN++
			s.live[fmt.Sprintf("%s-%d", s.child, s.lastN)] = &build{planKey: s.child, number: s.lastN,
				started: s.now().Add(s.queued + s.running), parent: resultKey, fails: s.childFails}
		}
		s.mu.Unlock()
		log.Printf("trigger %s with %d variables", resultKey, len(r.PostForm))
		// A real Bamboo Data Center reports "Custom revision build" only
		// when it honored customRevision; for any other reason bam's
		// Trigger reports the revision as ignored and app.Run stops the
		// build.
		reason := "Manual build"
		if r.URL.Query().Get("customRevision") != "" {
			reason = "Custom revision build"
		}
		out, _ := json.Marshal(map[string]any{
			"planKey": key, "buildNumber": num, "buildResultKey": resultKey,
			"triggerReason": reason,
		})
		writeJSON(w, out)
	case http.MethodDelete:
		s.mu.Lock()
		b := s.live[key]
		if b != nil {
			b.stopped = s.now()
			// A stopped build never ends, so it queues no child.
			for k, c := range s.live {
				if c.parent == key && !s.visible(c) {
					delete(s.live, k)
				}
			}
		}
		s.mu.Unlock()
		if b == nil {
			http.NotFound(w, r)
			return
		}
		log.Printf("cancel %s", key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *stub) handlePlan(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, api+"/plan/")
	key, sub, _ := strings.Cut(rest, "/")
	switch sub {
	case "":
		s.serve(w, "plan.json", fixturePlan, key)
	case "branch":
		s.serve(w, "branches.json", fixturePlan, key)
	case "variables":
		s.serve(w, "plan_variables.json", "", "")
	default:
		// "variable" is the other path Bamboo versions use for plan
		// variables; refusing it keeps the capability probe honest.
		http.NotFound(w, r)
	}
}

func (s *stub) handleProject(w http.ResponseWriter, r *http.Request) {
	key := strings.Trim(strings.TrimPrefix(r.URL.Path, api+"/project"), "/")
	if key == "" {
		s.serve(w, "projects.json", "", "")
		return
	}
	s.serve(w, "project_plans.json", fixtureProj, key)
}

func (s *stub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(api+"/info", func(w http.ResponseWriter, _ *http.Request) { s.serve(w, "info.json", "", "") })
	mux.HandleFunc(api+"/currentUser", func(w http.ResponseWriter, _ *http.Request) { s.serve(w, "currentUser.json", "", "") })
	mux.HandleFunc(api+"/project", s.handleProject)
	mux.HandleFunc(api+"/project/", s.handleProject)
	mux.HandleFunc(api+"/plan/", s.handlePlan)
	mux.HandleFunc(api+"/result/status/", s.handleStatus)
	mux.HandleFunc(api+"/result/", s.handleResult)
	mux.HandleFunc(api+"/queue/", s.handleQueue)
	mux.HandleFunc("/download/", func(w http.ResponseWriter, _ *http.Request) {
		body := s.fixture(w, filepath.Join("recorded", "log_download.log"))
		if body == nil {
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("no route for %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// bam sends a bearer token on every request; any value is accepted,
		// but its absence must still look like Bamboo's 401.
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7990", "listen address")
	fixtures := flag.String("fixtures", filepath.Join("internal", "provider", "bamboo", "testdata"), "fixture directory")
	s := newStub("")
	flag.DurationVar(&s.queued, "queued", s.queued, "how long a triggered build stays queued")
	flag.DurationVar(&s.running, "running", s.running, "how long a triggered build then runs")
	flag.StringVar(&s.child, "child", "", "plan a triggered build queues a child build of when it ends")
	flag.BoolVar(&s.childFails, "child-fails", false, "that child build fails")
	flag.Parse()
	s.fixtures = *fixtures

	if _, err := os.Stat(filepath.Join(s.fixtures, "info.json")); err != nil {
		log.Fatalf("no fixtures in %s: run this from the repository root, or pass -fixtures", s.fixtures)
	}
	log.Printf("fake Bamboo on http://%s, fixtures from %s", *addr, s.fixtures)
	log.Printf("drive it with: BAM_URL=http://%s BAM_TOKEN=devtoken bam", *addr)
	srv := &http.Server{Addr: *addr, Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
