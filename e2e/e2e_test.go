//go:build e2e

// Package e2e runs bam against a real Bamboo. The server and its token come
// from your bam configuration (the alias in ~/.config/bam/config.yaml and
// the token stored by "bam login"); the plan comes from a flag or from a
// configured target. The suite triggers one build, so name a plan that is
// safe to run (a smoke plan).
//
//	make e2e ARGS='-target smoke'
//	make e2e ARGS='-server lab -plan LAB-SMOKE -revision abc1234'
//	make e2e ARGS='-run TestSetSmoke -set-plans LAB-SPEC,LAB-SMOKE'
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/cli"
	"github.com/r0jjames/bam-cli/internal/toolcfg"
)

var (
	serverAlias = flag.String("server", "", "server alias (default: the one bam would use in this directory)")
	targetName  = flag.String("target", "", "configured target to take the plan key from")
	planKey     = flag.String("plan", "", "plan key that is safe to run (wins over -target)")
	revision    = flag.String("revision", "", "an older commit of the plan's repository; runs the plan at it and checks the build used it")
	setPlans    = flag.String("set-plans", "", "comma-separated plan keys that are safe to run; TestSetSmoke runs them as a set, one stage each")
)

func TestSmoke(t *testing.T) {
	if *planKey == "" && *targetName == "" {
		t.Skip("pass -plan KEY or -target NAME, e.g. make e2e ARGS='-target smoke'")
	}
	opts, err := toolcfg.SystemOptions()
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	cfg, err := toolcfg.Load(opts)
	if err != nil {
		t.Fatalf("read bam config: %v", err)
	}
	plan, targetServer := *planKey, ""
	if plan == "" {
		plan, targetServer, err = cfg.PlanFor(*targetName)
		if err != nil {
			t.Fatalf("target %s: %v", *targetName, err)
		}
	}
	server, err := cfg.ServerFor(*serverAlias, targetServer)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Logf("e2e against %s (%s), plan %s", server.Alias, server.URL, plan)
	run := bam(t, server.URL, server.Token, t.TempDir())

	if code, out, _ := run("whoami"); code != 0 || !strings.Contains(out, "env") {
		t.Fatalf("whoami failed")
	}
	if code, _, _ := run("doctor", "--plan", plan); code != 0 {
		t.Fatalf("doctor failed")
	}
	for _, args := range [][]string{{"plan", "show", plan}, {"plan", "vars", plan}, {"build", "list", plan}, {"build", "list", plan, "--json"}} {
		if code, _, _ := run(args...); code != 0 {
			t.Fatalf("%v failed", args)
		}
	}
	code, _, _ := run("run", plan, "--watch", "--timeout", "20m")
	if code != 0 && code != 1 {
		t.Fatalf("run --watch exited %d; want 0 or 1 (the build result)", code)
	}
	if code, out, _ := run("url", "--last"); code != 0 || !strings.Contains(out, "/browse/") {
		t.Fatalf("url --last failed")
	}
	if code, _, _ := run("logs", "--last"); code != 0 {
		t.Fatalf("logs --last failed")
	}
	if *revision != "" {
		code, _, _ := run("run", plan, "--revision", *revision, "--watch", "--timeout", "20m")
		if code != 0 && code != 1 {
			t.Fatalf("run --revision --watch exited %d; want 0 or 1", code)
		}
		code, out, _ := run("build", "show", "--last", "--json")
		if code != 0 || !strings.Contains(out, `"revision": "`+*revision) {
			t.Fatalf("build show --last does not report revision %s", *revision)
		}
	}
}

// bam returns a runner for bam in a throwaway home dir. The suite never uses
// the developer's own config files: the resolved server is handed over as
// the ad-hoc BAM_URL server.
func bam(t *testing.T, url, token, dir string) func(args ...string) (int, string, string) {
	return func(args ...string) (int, string, string) {
		t.Helper()
		env := cli.SystemEnv()
		var out, errOut bytes.Buffer
		env.Stdout, env.Stderr, env.StdoutTTY, env.StdinTTY = &out, &errOut, false, false
		env.WorkDir, env.Home = dir, dir
		env.Paths = cli.Paths{
			MachineConfig: filepath.Join(dir, "config.yaml"),
			Credentials:   filepath.Join(dir, "credentials.yaml"),
			Capabilities:  filepath.Join(dir, "capabilities.json"),
			State:         filepath.Join(dir, "state.json"),
			Sets:          filepath.Join(dir, "sets"),
		}
		env.Getenv = func(k string) string {
			switch k {
			case "BAM_URL":
				return url
			case "BAM_TOKEN":
				return token
			}
			return ""
		}
		code := cli.Execute(context.Background(), args, env)
		t.Logf("bam %s → %d\n%s%s", strings.Join(args, " "), code, out.String(), errOut.String())
		return code, out.String(), errOut.String()
	}
}

// TestSetSmoke runs the -set-plans plans as a set, one plan per stage, and
// checks the record, status and NDJSON output. It triggers one build per plan.
func TestSetSmoke(t *testing.T) {
	if *setPlans == "" {
		t.Skip("pass -set-plans KEY1,KEY2, e.g. make e2e ARGS='-run TestSetSmoke -set-plans LAB-SPEC,LAB-SMOKE'")
	}
	opts, err := toolcfg.SystemOptions()
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	cfg, err := toolcfg.Load(opts)
	if err != nil {
		t.Fatalf("read bam config: %v", err)
	}
	server, err := cfg.ServerFor(*serverAlias, "")
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	dir := t.TempDir()
	var yaml strings.Builder
	yaml.WriteString("version: 1\ntargets:\n")
	var stages []string
	for i, plan := range strings.Split(*setPlans, ",") {
		fmt.Fprintf(&yaml, "  run%d: {plan: %s}\n", i+1, strings.TrimSpace(plan))
		stages = append(stages, fmt.Sprintf("[run%d]", i+1))
	}
	fmt.Fprintf(&yaml, "sets:\n  e2e-train:\n    timeout: 40m\n    stages: [%s]\n", strings.Join(stages, ", "))
	if err := os.WriteFile(filepath.Join(dir, ".bam.yaml"), []byte(yaml.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("e2e set against %s (%s), stages %s", server.Alias, server.URL, strings.Join(stages, " "))
	run := bam(t, server.URL, server.Token, dir)

	if code, out, _ := run("set", "run", "e2e-train", "--dry-run"); code != 0 || !strings.Contains(out, "dry run") {
		t.Fatalf("set run --dry-run failed")
	}
	setCode, out, _ := run("set", "run", "e2e-train", "--json")
	if setCode != 0 && setCode != 1 {
		t.Fatalf("set run exited %d; want 0 or 1 (the set result)", setCode)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil || last["type"] != "set_done" {
		t.Fatalf("last NDJSON line is not set_done: %s", lines[len(lines)-1])
	}
	code, out, _ := run("set", "status", "--json")
	if code != 0 {
		t.Fatalf("set status failed")
	}
	var rec struct {
		Status string `json:"status"`
		Stages []struct {
			Runs []struct {
				BuildKey string `json:"build_key"`
			} `json:"runs"`
		} `json:"stages"`
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("set status --json: %v", err)
	}
	want := map[int]string{0: "successful", 1: "failed"}[setCode]
	if rec.Status != want || len(rec.Stages) == 0 || rec.Stages[0].Runs[0].BuildKey == "" {
		t.Fatalf("record %+v; want status %s and a first build key", rec, want)
	}
}
