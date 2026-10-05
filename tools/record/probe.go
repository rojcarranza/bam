package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// wadlSummary is what -probe-run learns from Bamboo's application.wadl:
// every method under the queue resource with its query parameters, and
// every method anywhere that takes a parameter whose name mentions verbose.
type wadlSummary struct {
	Queue   []string
	Verbose []string
}

type wadlParam struct {
	Name  string `xml:"name,attr"`
	Style string `xml:"style,attr"`
}

type wadlMethod struct {
	Name    string      `xml:"name,attr"`
	Request []wadlParam `xml:"request>param"`
}

type wadlResource struct {
	Path      string         `xml:"path,attr"`
	Methods   []wadlMethod   `xml:"method"`
	Resources []wadlResource `xml:"resource"`
}

type wadlApplication struct {
	Resources []wadlResource `xml:"resources>resource"`
}

var verboseRe = regexp.MustCompile(`(?i)verbose`)

// summarizeWADL reads a WADL document into a host-free text summary.
func summarizeWADL(body []byte) (wadlSummary, error) {
	var app wadlApplication
	if err := xml.Unmarshal(body, &app); err != nil {
		return wadlSummary{}, fmt.Errorf("parse WADL: %w", err)
	}
	var s wadlSummary
	var walk func(prefix string, res []wadlResource)
	walk = func(prefix string, res []wadlResource) {
		for _, r := range res {
			path := strings.Trim(r.Path, "/")
			if prefix != "" {
				path = prefix + "/" + path
			}
			for _, m := range r.Methods {
				var params, verbose []string
				for _, p := range m.Request {
					if p.Style != "query" {
						continue
					}
					params = append(params, p.Name)
					if verboseRe.MatchString(p.Name) {
						verbose = append(verbose, p.Name)
					}
				}
				line := m.Name + " " + path
				if path == "queue" || strings.HasPrefix(path, "queue/") {
					if len(params) > 0 {
						s.Queue = append(s.Queue, line+": "+strings.Join(params, " "))
					} else {
						s.Queue = append(s.Queue, line)
					}
				}
				if len(verbose) > 0 {
					s.Verbose = append(s.Verbose, line+": "+strings.Join(verbose, " "))
				}
			}
			walk(path, r.Resources)
		}
	}
	walk("", app.Resources)
	return s, nil
}

// olderRevision returns the first revision in a newest-first list that
// differs from the newest one, or "" when every build used the same one.
func olderRevision(newestFirst []string) string {
	for _, r := range newestFirst {
		if r != newestFirst[0] {
			return r
		}
	}
	return ""
}

// invalidRevision is a revision no repository has, to learn whether Bamboo
// refuses an unknown customRevision or quietly builds the newest commit.
const invalidRevision = "0000000000000000000000000000000000000000"

// probeRun learns whether the queue endpoint supports a custom revision and
// verbose logs. It triggers plan once per probe and waits for each build to
// finish, so pass a plan that is safe to run.
func (r *recorder) probeRun(plan string) error {
	body, status, err := r.callAccept(http.MethodGet, "/rest/api/latest/application.wadl", nil, "application/vnd.sun.wadl+xml, application/xml")
	if err != nil || status != 200 {
		return fmt.Errorf("application.wadl: status %d: %v", status, err)
	}
	s, err := summarizeWADL(body)
	if err != nil {
		return err
	}
	summary := "# queue resource\n" + strings.Join(s.Queue, "\n") + "\n# parameters mentioning verbose\n" + strings.Join(s.Verbose, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(r.out, "wadl_queue.txt"), r.scrub.Scrub([]byte(summary)), 0o644); err != nil {
		return err
	}
	fmt.Printf("%-28s %d\n%s", "wadl_queue.txt", status, summary)

	revs, err := r.revisions(plan)
	if err != nil {
		return err
	}
	if len(revs) == 0 {
		return fmt.Errorf("plan %s has no build with a revision; run it once first", plan)
	}
	older := olderRevision(revs)
	if older == "" {
		fmt.Println("every recent build used one revision; the probe can show only that customRevision is accepted")
		older = revs[0]
	}
	if err := r.probeTrigger(plan, "revision", url.Values{"customRevision": {older}}, older); err != nil {
		return err
	}
	// A short SHA is what people paste from git log --oneline.
	if len(older) > 7 {
		if err := r.probeTrigger(plan, "revision_short", url.Values{"customRevision": {older[:7]}}, older); err != nil {
			return err
		}
	}
	if err := r.probeTrigger(plan, "revision_invalid", url.Values{"customRevision": {invalidRevision}}, invalidRevision); err != nil {
		return err
	}

	param := verboseQueueParam(s)
	if param == "" {
		fmt.Println("no queue parameter mentions verbose; not triggering a verbose probe")
		return nil
	}
	return r.probeTrigger(plan, "verbose", url.Values{param: {"true"}}, "")
}

// verboseQueueParam returns a verbose parameter of the build queue's POST
// method. The deployment queue (POST queue/deployment) is a different
// endpoint and does not count.
func verboseQueueParam(s wadlSummary) string {
	for _, line := range s.Verbose {
		if !strings.HasPrefix(line, "POST queue/{") {
			continue
		}
		// Path templates can hold ": " too, so the parameters follow the last one.
		if i := strings.LastIndex(line, ": "); i >= 0 {
			return strings.Fields(line[i+2:])[0]
		}
	}
	return ""
}

// revisions lists the first repository revision of the plan's recent
// builds, newest first.
func (r *recorder) revisions(plan string) ([]string, error) {
	body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+plan,
		url.Values{"expand": {"results.result.vcsRevisions"}, "max-result": {"25"}})
	if err != nil || status != 200 {
		return nil, fmt.Errorf("revisions of %s: status %d: %v", plan, status, err)
	}
	var res struct {
		Results struct {
			Result []struct {
				VcsRevisions struct {
					VcsRevision []struct {
						Key string `json:"vcsRevisionKey"`
					} `json:"vcsRevision"`
				} `json:"vcsRevisions"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, b := range res.Results.Result {
		if len(b.VcsRevisions.VcsRevision) > 0 {
			out = append(out, b.VcsRevisions.VcsRevision[0].Key)
		}
	}
	return out, nil
}

// probeTrigger queues plan with extra query parameters, records the queue
// answer as queue_<name>.json (or .status), waits for the build to finish,
// and records it as result_<name>.json. want, when set, is the revision the
// build should have used.
func (r *recorder) probeTrigger(plan, name string, extra url.Values, want string) error {
	q := url.Values{"executeAllStages": {"true"}}
	for k, v := range extra {
		q[k] = v
	}
	body, status, err := r.call(http.MethodPost, "/rest/api/latest/queue/"+plan, q)
	if err != nil {
		return fmt.Errorf("trigger %s (%s): %w", plan, name, err)
	}
	file := "queue_" + name + ".json"
	if status/100 != 2 {
		fmt.Printf("%-28s %d (recorded as .status): Bamboo refused the trigger\n", file, status)
		snippet := r.scrub.Scrub(body)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return os.WriteFile(filepath.Join(r.out, file+".status"), []byte(fmt.Sprintf("%d\n%s\n", status, snippet)), 0o644)
	}
	fmt.Printf("%-28s %d\n", file, status)
	if err := writeScrubbed(r.out, file, body, r.scrub); err != nil {
		return err
	}
	var queued struct {
		Key string `json:"buildResultKey"`
	}
	if err := json.Unmarshal(body, &queued); err != nil || queued.Key == "" {
		return errors.New("queue answer has no buildResultKey")
	}

	expand := url.Values{"expand": {"stages.stage.results.result,vcsRevisions"}}
	var state string
	for i := 0; i < 150 && state != "Finished" && state != "NotBuilt"; i++ {
		time.Sleep(2 * time.Second)
		body, status, err = r.call(http.MethodGet, "/rest/api/latest/result/"+queued.Key, expand)
		if err == nil && status == 200 {
			state = lifeCycleState(body)
		}
	}
	if state != "Finished" && state != "NotBuilt" {
		return fmt.Errorf("%s did not finish within 5 minutes", queued.Key)
	}
	if err := r.record("result_"+name+".json", http.MethodGet, "/rest/api/latest/result/"+queued.Key, expand); err != nil {
		return err
	}
	var res struct {
		State        string `json:"buildState"`
		VcsRevisions struct {
			VcsRevision []struct {
				Key string `json:"vcsRevisionKey"`
			} `json:"vcsRevision"`
		} `json:"vcsRevisions"`
	}
	_ = json.Unmarshal(body, &res)
	built := ""
	if len(res.VcsRevisions.VcsRevision) > 0 {
		built = res.VcsRevisions.VcsRevision[0].Key
	}
	fmt.Printf("  %s %s (%s): state %s, built revision %q", queued.Key, name, state, res.State, built)
	if want != "" {
		fmt.Printf(", requested %q, match %v", want, built == want)
	}
	fmt.Println()
	return nil
}

// probeRerun triggers plan and waits for the build. If it failed, it
// restarts it with PUT queue/{key} and records the answer, then the result
// at each change of state until it finishes again. Last, it restarts the
// newest successful build of plan, to record how Bamboo refuses.
func (r *recorder) probeRerun(plan string) error {
	body, status, err := r.call(http.MethodPost, "/rest/api/latest/queue/"+plan, url.Values{"executeAllStages": {"true"}})
	if err != nil || status/100 != 2 {
		return fmt.Errorf("trigger %s: status %d: %v", plan, status, err)
	}
	var queued struct {
		Key string `json:"buildResultKey"`
	}
	if err := json.Unmarshal(body, &queued); err != nil || queued.Key == "" {
		return errors.New("queue answer has no buildResultKey")
	}
	life, state, err := r.waitFinished(queued.Key)
	if err != nil {
		return err
	}
	if state == "Failed" {
		if err := r.restart(queued.Key, "queue_restart.json"); err != nil {
			return err
		}
		if err := r.recordRestart(queued.Key); err != nil {
			return err
		}
	} else {
		fmt.Printf("%s ended %s/%s; a restart needs a failed build, so pass a plan that fails\n", queued.Key, life, state)
	}
	ok, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+plan, url.Values{"buildstate": {"Successful"}, "max-result": {"1"}})
	if err != nil || status != 200 {
		return fmt.Errorf("successful builds of %s: status %d: %v", plan, status, err)
	}
	var list struct {
		Results struct {
			Result []struct {
				Key string `json:"buildResultKey"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal(ok, &list); err != nil || len(list.Results.Result) == 0 {
		fmt.Printf("%s has no successful build; not recording a refusal\n", plan)
		return nil
	}
	return r.restart(list.Results.Result[0].Key, "queue_restart_refused.json")
}

// restart sends PUT queue/{key} and records the answer as file, or, when
// Bamboo refuses, its status and message as file.status.
func (r *recorder) restart(key, file string) error {
	body, status, err := r.call(http.MethodPut, "/rest/api/latest/queue/"+key, url.Values{"executeAllStages": {"true"}})
	if err != nil {
		return fmt.Errorf("restart %s: %w", key, err)
	}
	if status/100 != 2 {
		fmt.Printf("%-28s %d (recorded as .status): Bamboo refused the restart\n", file, status)
		snippet := r.scrub.Scrub(body)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return os.WriteFile(filepath.Join(r.out, file+".status"), []byte(fmt.Sprintf("%d\n%s\n", status, snippet)), 0o644)
	}
	fmt.Printf("%-28s %d\n", file, status)
	return writeScrubbed(r.out, file, body, r.scrub)
}

// recordRestart records result_restart_N.json at once and at each change of
// lifeCycleState/buildState, until the build finishes again or 5 minutes pass.
func (r *recorder) recordRestart(key string) error {
	expand := url.Values{"expand": {"stages.stage.results.result,vcsRevisions"}}
	last, n := "", 0
	for i := 0; i < 150; i++ {
		body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+key, expand)
		if err == nil && status == 200 {
			life, state := resultStates(body)
			if now := life + "/" + state; now != last {
				if err := r.record(fmt.Sprintf("result_restart_%d.json", n), http.MethodGet, "/rest/api/latest/result/"+key, expand); err != nil {
					return err
				}
				last, n = now, n+1
				if i > 0 && life == "Finished" {
					return nil
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("%s did not finish again within 5 minutes", key)
}

// waitFinished polls key until it is Finished or NotBuilt.
func (r *recorder) waitFinished(key string) (life, state string, err error) {
	for i := 0; i < 150; i++ {
		time.Sleep(2 * time.Second)
		body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+key, nil)
		if err == nil && status == 200 {
			life, state = resultStates(body)
			if life == "Finished" || life == "NotBuilt" {
				return life, state, nil
			}
		}
	}
	return "", "", fmt.Errorf("%s did not finish within 5 minutes", key)
}

func resultStates(body []byte) (life, state string) {
	var res struct {
		Life  string `json:"lifeCycleState"`
		State string `json:"buildState"`
	}
	_ = json.Unmarshal(body, &res)
	return res.Life, res.State
}
