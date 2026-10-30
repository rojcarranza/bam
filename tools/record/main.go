// Command record captures Bamboo REST responses from a personal server,
// scrubs them, and writes them to internal/provider/bamboo/testdata/recorded.
//
// The server and its token come from bam's own configuration: the alias in
// ~/.config/bam/config.yaml (or .bam.yaml) and the token stored by
// "bam login". Nothing is exported into the environment.
//
//	go run ./tools/record -target provision
//	go run ./tools/record -server lab -plan LAB-PROV
//	go run ./tools/record -server lab -plan LAB-PROV -failed LAB-PROV-12 -trigger
//
// Flags:
//
//	-server ALIAS   server alias; default: the one bam would use here
//	-target NAME    take the plan key from this configured target
//	-plan KEY       plan key with builds, e.g. LAB-PROV (wins over -target)
//	-failed KEY     failed build to record logs from; default: the newest
//	                failed build of the plan, "skip" to record none
//	-trigger        also trigger a build, stop it, and record both
//	-as KEY         placeholder project key in the recording (default LAB)
//	-probe-run      read the queue endpoint's parameters from the WADL, then
//	                trigger the plan with a custom revision, its short
//	                form, an unknown revision, and a verbose-logs parameter
//	                if the WADL has one, waiting for each build to finish
//	-probe-rerun    trigger the plan, and if the build fails restart it with
//	                PUT queue/{key}, recording the answer and every state
//	                change until it finishes; then record how Bamboo refuses
//	                to restart a successful build
//	-probe-plans    record the first and second page of the plan's project
//	                (expand plans.plan, then plans[25:125].plan) and the
//	                second page of GET plan, with every plan replaced by a
//	                synthetic one; triggers nothing
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/r0jjames/bam-cli/internal/toolcfg"
)

type recorder struct {
	base, token, out string
	http             *http.Client
	scrub            bamboo.Scrubber
}

// variableValueFiles are the recorded files whose variable values (not
// names) must be replaced with synthetic placeholders before they can be
// committed: they can hold values from a personal Bamboo server.
var variableValueFiles = map[string]bool{
	"plan_variables.json":   true,
	"plan_variable.json":    true,
	"result_variables.json": true,
}

// prettyJSON indents v without escaping <, > and &. The escaped forms end in
// a letter (\u003e), which would hide a name that follows a tag from the
// scrubber's word boundary and leak it into a recording.
func prettyJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// scrubVariableValues decodes body as arbitrary JSON and, for every object
// that looks like a Bamboo variable entry (it has a "name" or "key" field
// alongside "value"), replaces a non-masked "value" with a synthetic
// "value-N". Bamboo's own mask ("********") is left as is. N counts across
// the whole document in encounter order.
func scrubVariableValues(body []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	n := 0
	v = rewriteVariableValues(v, &n)
	return prettyJSON(v)
}

func rewriteVariableValues(v any, n *int) any {
	switch t := v.(type) {
	case []any:
		for i, item := range t {
			t[i] = rewriteVariableValues(item, n)
		}
		return t
	case map[string]any:
		_, hasName := t["name"]
		_, hasKey := t["key"]
		if val, ok := t["value"].(string); ok && (hasName || hasKey) && val != bamboo.MaskedValue {
			*n++
			t["value"] = fmt.Sprintf("value-%d", *n)
		}
		for k, val := range t {
			t[k] = rewriteVariableValues(val, n)
		}
		return t
	default:
		return v
	}
}

// maxScrubbedLogLines is how many log entries/lines a recording keeps; the
// rest are dropped so a long build log never carries private data past what
// review can reasonably catch.
const maxScrubbedLogLines = 5

// scrubLogEntries truncates a recorded "logEntries" envelope (as returned by
// GET .../result/{key}?expand=logEntries[...]) to at most the first
// maxScrubbedLogLines entries and replaces each entry's log text with a
// synthetic "log line N".
func scrubLogEntries(body []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	wrapper, ok := root["logEntries"].(map[string]any)
	if !ok {
		return prettyJSON(root)
	}
	var listKey string
	var list []any
	for k, val := range wrapper {
		if arr, ok := val.([]any); ok {
			listKey, list = k, arr
			break
		}
	}
	if listKey == "" {
		return prettyJSON(root)
	}
	if len(list) > maxScrubbedLogLines {
		list = list[:maxScrubbedLogLines]
	}
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text := fmt.Sprintf("log line %d", i+1)
		if _, ok := entry["log"]; ok {
			entry["log"] = text
		}
		if _, ok := entry["unstyledLog"]; ok {
			entry["unstyledLog"] = text
		}
	}
	wrapper[listKey] = list
	if _, ok := wrapper["size"]; ok {
		wrapper["size"] = len(list)
	}
	root["logEntries"] = wrapper
	return prettyJSON(root)
}

// scrubLogDownload truncates a raw log download (lines shaped
// "type<TAB>date<TAB>message") to at most the first maxScrubbedLogLines
// lines, keeping the type/date prefix but replacing the message with a
// synthetic "log line N".
func scrubLogDownload(body []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(body), "\r\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	if len(lines) > maxScrubbedLogLines {
		lines = lines[:maxScrubbedLogLines]
	}
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		text := fmt.Sprintf("log line %d", i+1)
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) == 3 {
			lines[i] = parts[0] + "\t" + parts[1] + "\t" + text
		} else {
			lines[i] = text
		}
	}
	out := strings.Join(lines, "\n")
	if out != "" {
		out += "\n"
	}
	return []byte(out)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "record:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		serverAlias = flag.String("server", "", "server alias (default: the one bam would use in this directory)")
		targetName  = flag.String("target", "", "configured target to take the plan key from")
		planKey     = flag.String("plan", "", "plan key with builds, e.g. LAB-PROV")
		failedKey   = flag.String("failed", "", `failed build to record logs from ("skip" to record none; default: the newest failed build)`)
		trigger     = flag.Bool("trigger", false, "also trigger a build, stop it, and record both")
		asKey       = flag.String("as", "LAB", "placeholder project key the recorded project is renamed to")
		probe       = flag.Bool("probe-run", false, "probe custom revision and verbose logs support; triggers the plan up to three times")
		probeRerun  = flag.Bool("probe-rerun", false, "probe build restart (PUT queue/{key}); triggers the plan once and restarts it if it failed")
		probeKids   = flag.Bool("probe-children", false, "probe how Bamboo reports the child builds of the plan's newest build; triggers nothing")
		probePlans  = flag.Bool("probe-plans", false, "probe how Bamboo pages the plans of the plan's project, with synthetic plan names; triggers nothing")
	)
	flag.Parse()

	opts, err := toolcfg.SystemOptions()
	if err != nil {
		return err
	}
	if err := moveLegacyConfig(os.Stderr, opts, legacyConfigDirs()...); err != nil {
		return err
	}
	cfg, err := toolcfg.Load(opts)
	if err != nil {
		return err
	}
	plan, targetServer := *planKey, ""
	if plan == "" {
		if *targetName == "" {
			return errors.New("pass -plan KEY or -target NAME (the server and token come from your bam config)")
		}
		plan, targetServer, err = cfg.PlanFor(*targetName)
		if err != nil {
			return err
		}
	}
	server, err := cfg.ServerFor(*serverAlias, targetServer)
	if err != nil {
		return err
	}
	u, err := url.Parse(server.URL)
	if err != nil {
		return err
	}
	fmt.Printf("recording %s from %s (%s)\n", plan, server.Alias, server.URL)

	r := &recorder{base: server.URL, token: server.Token, http: &http.Client{Timeout: 30 * time.Second},
		out: filepath.Join("internal", "provider", "bamboo", "testdata", "recorded")}
	if err := os.MkdirAll(r.out, 0o755); err != nil {
		return err
	}

	var me struct {
		Name     string `json:"name"`
		FullName string `json:"fullName"`
	}
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/currentUser", nil)
	if err != nil || status != 200 {
		return fmt.Errorf("currentUser: status %d: %v", status, err)
	}
	_ = json.Unmarshal(body, &me)

	project := strings.SplitN(plan, "-", 2)[0]
	latest, err := r.latestBuild(plan)
	if err != nil && !*probePlans { // the plans probe reads no build
		return err
	}
	names, err := r.placeholderNames(project, latest, *asKey)
	if err != nil {
		return err
	}
	r.scrub = bamboo.Scrubber{Host: u.Host, Users: []string{me.FullName, me.Name}, Names: names}

	// Update the fixture denylist with terms from this recording
	if err := r.updateDenylist(); err != nil {
		return err
	}
	// The probe runs on a plan that is safe to trigger, usually not the one
	// the base recordings come from, so it records its own files only.
	if *probe {
		if err := r.probeRun(plan); err != nil {
			return err
		}
		return r.finish()
	}
	if *probeRerun {
		if err := r.probeRerun(plan); err != nil {
			return err
		}
		return r.finish()
	}
	if *probeKids {
		if err := r.probeChildren(plan); err != nil {
			return err
		}
		return r.finish()
	}
	if *probePlans {
		if err := r.probePlans(project); err != nil {
			return err
		}
		return r.finish()
	}
	steps := []struct {
		file, path string
		query      url.Values
	}{
		{"currentUser.json", "/rest/api/latest/currentUser", nil},
		{"info.json", "/rest/api/latest/info", nil},
		{"projects.json", "/rest/api/latest/project", url.Values{"max-result": {"25"}}},
		{"project_plans.json", "/rest/api/latest/project/" + project, url.Values{"expand": {"plans.plan"}}},
		{"plan.json", "/rest/api/latest/plan/" + plan, nil},
		{"plan_variables.json", "/rest/api/latest/plan/" + plan + "/variables", nil},
		{"plan_variable.json", "/rest/api/latest/plan/" + plan + "/variable", nil},
		{"branches.json", "/rest/api/latest/plan/" + plan + "/branch", url.Values{"max-result": {"25"}}},
		{"results.json", "/rest/api/latest/result/" + plan, url.Values{"expand": {"results.result"}, "max-result": {"5"}}},
		{"result_detail.json", "/rest/api/latest/result/" + latest, url.Values{"expand": {"stages.stage.results.result,labels,vcsRevisions"}}},
		{"result_variables.json", "/rest/api/latest/result/" + latest, url.Values{"expand": {"variables"}}},
	}
	for _, s := range steps {
		if err := r.record(s.file, http.MethodGet, s.path, s.query); err != nil {
			return err
		}
	}

	failed := *failedKey
	if failed == "" {
		failed, err = r.latestFailedBuild(plan)
		if err != nil {
			return err
		}
		if failed == "" {
			fmt.Println("no failed build found in the last 25 results; pass -failed KEY to record one")
		}
	}
	if failed != "" && failed != "skip" {
		if err := r.recordFailed(failed); err != nil {
			return err
		}
	}
	if *trigger {
		if err := r.recordTriggerAndStop(plan); err != nil {
			return err
		}
	}
	return r.finish()
}

// finish warns about names the scrubber left and reminds to review.
func (r *recorder) finish() error {
	if err := r.warnResidual(); err != nil {
		return err
	}
	fmt.Println("recorded into", r.out, "- review every file before committing")
	return nil
}

func (r *recorder) call(method, path string, q url.Values) ([]byte, int, error) {
	accept := "application/json"
	if strings.HasPrefix(path, "/download/") {
		accept = ""
	}
	return r.callAccept(method, path, q, accept)
}

// callAccept is call with an explicit Accept header; "" sends none.
func (r *recorder) callAccept(method, path string, q url.Values, accept string) ([]byte, int, error) {
	full := r.base + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	req, err := http.NewRequest(method, full, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// record writes the scrubbed body to file, or file+".status" for non-2xx.
func (r *recorder) record(file, method, path string, q url.Values) error {
	body, status, err := r.call(method, path, q)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	if status < 200 || status > 299 {
		snippet := r.scrub.Scrub(body)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		fmt.Printf("%-28s %d (recorded as .status)\n", file, status)
		return os.WriteFile(filepath.Join(r.out, file+".status"), []byte(fmt.Sprintf("%d\n%s\n", status, snippet)), 0o644)
	}
	fmt.Printf("%-28s %d\n", file, status)
	return writeScrubbed(r.out, file, body, r.scrub)
}

// writeScrubbed applies the file-specific scrubbing pass (pretty-printing,
// variable-value replacement, log truncation) and the host/user Scrubber,
// then writes the result to filepath.Join(dir, file).
//
// It fails closed: when a scrubbing step returns an error, writeScrubbed
// returns that error and writes nothing for file, rather than falling back
// to the raw (unscrubbed) body. A recording that cannot be scrubbed must
// never be committed.
func writeScrubbed(dir, file string, body []byte, scrub bamboo.Scrubber) error {
	if strings.HasSuffix(file, ".json") {
		var pretty any
		if json.Unmarshal(body, &pretty) == nil {
			body, _ = prettyJSON(pretty)
		}
	}
	switch {
	case variableValueFiles[file]:
		scrubbed, err := scrubVariableValues(body)
		if err != nil {
			return fmt.Errorf("scrub %s: %w", file, err)
		}
		body = scrubbed
	case file == "log_entries.json":
		scrubbed, err := scrubLogEntries(body)
		if err != nil {
			return fmt.Errorf("scrub %s: %w", file, err)
		}
		body = scrubbed
	case file == "log_download.log":
		body = scrubLogDownload(body)
	}
	return os.WriteFile(filepath.Join(dir, file), scrub.Scrub(body), 0o644)
}

// placeholderNames maps every project key and name on the server, and
// every repository name of the recorded build, to placeholders. The
// recorded project becomes `as` (LAB by default); every other project
// becomes LAB2, LAB3 and so on, because projects.json records the
// server's whole project list.
func (r *recorder) placeholderNames(project, build, as string) (map[string]string, error) {
	names := map[string]string{}
	lower := strings.ToLower(as)
	add := func(real, placeholder string) {
		if real == "" || names[real] != "" {
			return
		}
		names[real] = placeholder
	}

	add(project, as)
	name, err := r.projectName(project)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(name, project) {
		add(name, lower)
	}

	others, err := r.otherProjects(project)
	if err != nil {
		return nil, err
	}
	for i, p := range others {
		add(p.Key, fmt.Sprintf("%s%d", as, i+2))
		if !strings.EqualFold(p.Name, p.Key) {
			add(p.Name, fmt.Sprintf("%s%d", lower, i+2))
		}
	}

	var repos []string
	if build != "" {
		repos, err = r.repositoryNames(build)
		if err != nil {
			return nil, err
		}
	}
	for i, repo := range repos {
		add(repo, fmt.Sprintf("%s-repo-%d", lower, i+1))
	}
	return names, nil
}

func (r *recorder) projectName(key string) (string, error) {
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/project/"+key, nil)
	if err != nil || status != 200 {
		return "", fmt.Errorf("project %s: status %d: %v", key, status, err)
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", err
	}
	return p.Name, nil
}

type projectRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// otherProjects lists every project on the server except the recorded one.
func (r *recorder) otherProjects(recorded string) ([]projectRef, error) {
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/project", url.Values{"max-result": {"25"}})
	if err != nil || status != 200 {
		return nil, fmt.Errorf("projects: status %d: %v", status, err)
	}
	var res struct {
		Projects struct {
			Project []projectRef `json:"project"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	var out []projectRef
	for _, p := range res.Projects.Project {
		if !strings.EqualFold(p.Key, recorded) {
			out = append(out, p)
		}
	}
	return out, nil
}

// repositoryNames returns the repositories a build was built from.
func (r *recorder) repositoryNames(build string) ([]string, error) {
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+build, url.Values{"expand": {"vcsRevisions"}})
	if err != nil || status != 200 {
		return nil, fmt.Errorf("vcs revisions of %s: status %d: %v", build, status, err)
	}
	var res struct {
		VCSRevisions struct {
			Revision []struct {
				RepositoryName string `json:"repositoryName"`
			} `json:"vcsRevision"`
		} `json:"vcsRevisions"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, rev := range res.VCSRevisions.Revision {
		if rev.RepositoryName != "" {
			out = append(out, rev.RepositoryName)
		}
	}
	return out, nil
}

// userRe finds the user names Bamboo puts in build reasons, so the recorder
// can report anyone the scrubber did not replace with jdoe.
var userRe = regexp.MustCompile(`/browse/user/([A-Za-z0-9._-]+)`)

// warnResidual reports names left in the recording that are not
// placeholders: another user who triggered one of the recorded builds, or a
// name too short to replace safely.
func (r *recorder) warnResidual() error {
	entries, err := os.ReadDir(r.out)
	if err != nil {
		return err
	}
	users := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.out, e.Name()))
		if err != nil {
			return err
		}
		for _, m := range userRe.FindAllStringSubmatch(string(data), -1) {
			if m[1] != "jdoe" {
				users[m[1]] = true
			}
		}
	}
	for _, name := range sortedNames(users) {
		fmt.Printf("WARNING: user name %q is still in the recording; scrub it by hand\n", name)
	}
	for _, name := range r.scrub.RiskyNames() {
		fmt.Printf("WARNING: %q is short enough to match unrelated words; check the recording\n", name)
	}
	return nil
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *recorder) latestBuild(plan string) (string, error) {
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+plan, url.Values{"max-result": {"1"}})
	if err != nil || status != 200 {
		return "", fmt.Errorf("latest build of %s: status %d: %v", plan, status, err)
	}
	var res struct {
		Results struct {
			Result []struct {
				Key string `json:"key"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &res); err != nil || len(res.Results.Result) == 0 {
		return "", fmt.Errorf("plan %s has no builds; run it once first", plan)
	}
	return res.Results.Result[0].Key, nil
}

// latestFailedBuild returns the newest failed build of plan, or "" when the
// recent results hold none.
func (r *recorder) latestFailedBuild(plan string) (string, error) {
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+plan,
		url.Values{"expand": {"results.result"}, "max-result": {"25"}})
	if err != nil || status != 200 {
		return "", fmt.Errorf("results of %s: status %d: %v", plan, status, err)
	}
	var res struct {
		Results struct {
			Result []struct {
				Key   string `json:"key"`
				State string `json:"state"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}
	for _, b := range res.Results.Result {
		if b.State == "Failed" {
			return b.Key, nil
		}
	}
	return "", nil
}

func (r *recorder) recordFailed(buildKey string) error {
	if err := r.record("failed_detail.json", http.MethodGet, "/rest/api/latest/result/"+buildKey,
		url.Values{"expand": {"stages.stage.results.result"}}); err != nil {
		return err
	}
	if err := r.record("failed_tests.json", http.MethodGet, "/rest/api/latest/result/"+buildKey,
		url.Values{"expand": {"testResults.failedTests.testResult"}}); err != nil {
		return err
	}
	body, _, err := r.call(http.MethodGet, "/rest/api/latest/result/"+buildKey, url.Values{"expand": {"stages.stage.results.result"}})
	if err != nil {
		return err
	}
	var res struct {
		Stages struct {
			Stage []struct {
				Results struct {
					Result []struct {
						Key   string `json:"buildResultKey"`
						State string `json:"state"`
					} `json:"result"`
				} `json:"results"`
			} `json:"stage"`
		} `json:"stages"`
	}
	_ = json.Unmarshal(body, &res)
	for _, s := range res.Stages.Stage {
		for _, j := range s.Results.Result {
			if j.State != "Failed" {
				continue
			}
			cut := strings.LastIndex(j.Key, "-")
			if cut <= 0 {
				continue
			}
			jobKey := j.Key[:cut]
			// Bamboo 12 reports failed tests on the job's result only, so
			// failedTests asks here when the build's own result has none.
			if err := r.record("job_failed_tests.json", http.MethodGet, "/rest/api/latest/result/"+j.Key,
				url.Values{"expand": {"testResults.failedTests.testResult"}}); err != nil {
				return err
			}
			if err := r.record("log_entries.json", http.MethodGet, "/rest/api/latest/result/"+j.Key,
				url.Values{"expand": {"logEntries[0:50]"}}); err != nil {
				return err
			}
			return r.record("log_download.log", http.MethodGet, "/download/"+jobKey+"/build_logs/"+j.Key+".log", nil)
		}
	}
	fmt.Println("no failed job found in", buildKey)
	return nil
}

// lifeCycleState reads the top-level lifeCycleState of a result document.
func lifeCycleState(body []byte) string {
	var res struct {
		LifeCycleState string `json:"lifeCycleState"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return ""
	}
	return res.LifeCycleState
}

// jobKeys returns the job result keys of a build that are not finished yet.
func (r *recorder) jobKeys(build string) ([]string, error) {
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+build, url.Values{"expand": {"stages.stage.results.result"}})
	if err != nil || status != 200 {
		return nil, fmt.Errorf("stages of %s: status %d: %v", build, status, err)
	}
	var res struct {
		Stages struct {
			Stage []struct {
				Results struct {
					Result []struct {
						Key            string `json:"buildResultKey"`
						LifeCycleState string `json:"lifeCycleState"`
					} `json:"result"`
				} `json:"results"`
			} `json:"stage"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	var keys []string
	for _, st := range res.Stages.Stage {
		for _, j := range st.Results.Result {
			if j.LifeCycleState != "Finished" && j.Key != "" {
				keys = append(keys, j.Key)
			}
		}
	}
	return keys, nil
}

func (r *recorder) recordTriggerAndStop(plan string) error {
	body, status, err := r.call(http.MethodPost, "/rest/api/latest/queue/"+plan, url.Values{"executeAllStages": {"true"}})
	if err != nil || status/100 != 2 {
		return fmt.Errorf("trigger %s: status %d: %v", plan, status, err)
	}
	if err := writeScrubbed(r.out, "queue.json", body, r.scrub); err != nil {
		return err
	}
	var q struct {
		Key string `json:"buildResultKey"`
	}
	_ = json.Unmarshal(body, &q)

	// Stop the build the way bam does: Bamboo takes a JOB result key on
	// the queue endpoint, not the plan-level build key.
	var statuses []string
	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Second)
		jobs, err := r.jobKeys(q.Key)
		if err != nil {
			return err
		}
		// The progress endpoint answers only while the build runs, so it is
		// recorded here, before the stop below settles it.
		if len(jobs) > 0 {
			if err := r.record("status_running.json", http.MethodGet, "/rest/api/latest/result/status/"+q.Key, nil); err != nil {
				return err
			}
		}
		for _, job := range jobs {
			_, status, err := r.call(http.MethodDelete, "/rest/api/latest/queue/"+job, nil)
			if err != nil {
				return err
			}
			statuses = append(statuses, fmt.Sprintf("%d", status))
		}
		if len(jobs) > 0 {
			break
		}
	}
	if err := os.WriteFile(filepath.Join(r.out, "stop.status"), []byte(strings.Join(statuses, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	// A stopped build settles as NotBuilt, not Finished.
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)
		body, status, err = r.call(http.MethodGet, "/rest/api/latest/result/"+q.Key, url.Values{"expand": {"stages.stage.results.result"}})
		if err != nil || status != 200 {
			continue
		}
		if state := lifeCycleState(body); state == "Finished" || state == "NotBuilt" {
			break
		}
	}
	return r.record("result_stopped.json", http.MethodGet, "/rest/api/latest/result/"+q.Key,
		url.Values{"expand": {"stages.stage.results.result"}})
}

// legacyConfigDirs are where older bam versions kept their config: adrg/xdg's
// config home, and for the denylist on Windows, os.UserConfigDir.
func legacyConfigDirs() []string {
	dirs := []string{filepath.Join(xdg.ConfigHome, "bam")}
	if dir, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(dir, "bam"))
	}
	return dirs
}

// moveLegacyConfig moves bam's config files out of the from directories, so
// the recorder reads the config bam would and adds to the existing denylist
// instead of starting a new one that leaves the old terms behind. A failed
// move stops the recording for the same reason.
func moveLegacyConfig(w io.Writer, o toolcfg.Options, from ...string) error {
	to := filepath.Dir(o.Credentials)
	for _, dir := range from {
		moved, err := config.MoveDir(dir, to, o.MachineFile, o.Getenv("BAM_FIXTURE_DENYLIST"))
		for _, name := range moved {
			fmt.Fprintf(w, "moved %s from %s to %s\n", name, dir, to)
		}
		if err != nil {
			return fmt.Errorf("move config from %s: %w", dir, err)
		}
	}
	return nil
}

// updateDenylist appends the scrubber's terms to the fixture denylist file.
func (r *recorder) updateDenylist() error {
	path := bamboo.DenylistPath(os.Getenv)
	if path == "" {
		return nil // Without a home directory, skip silently
	}

	// Create directory with 0700
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	// Load existing terms
	existing, err := bamboo.LoadDenylist(path)
	if err != nil {
		return err
	}
	existingMap := make(map[string]bool)
	for _, term := range existing {
		existingMap[term] = true
	}

	// Get new terms from scrubber
	newTerms := r.scrub.DenylistTerms()

	// Append only new terms
	var added []string
	for _, term := range newTerms {
		if !existingMap[term] {
			added = append(added, term)
			existingMap[term] = true
		}
	}

	if len(added) == 0 {
		return nil
	}

	// Read current content
	var content string
	if data, err := os.ReadFile(path); err == nil {
		content = string(data)
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
	}

	// Append new terms
	for _, term := range added {
		content += term + "\n"
	}

	// Write back with 0600
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}

	fmt.Printf("fixture denylist: added %d terms to %s\n", len(added), path)
	return nil
}
