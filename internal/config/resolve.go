package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
)

// EnvServerAlias names the ad-hoc server defined by BAM_URL.
const EnvServerAlias = "env"

// ResolvedServer is one server alias after merging both layers.
type ResolvedServer struct {
	Alias    string
	URL      string
	AuthEnv  string
	Token    string // only for the BAM_URL server
	Projects []string
	// DefaultProject is the server entries' default_project, as written.
	DefaultProject string
	DefinedIn      []string // "project", "machine", "env"
}

// Servers returns every configured alias, merged field by field with the
// machine layer winning, plus the BAM_URL server when set.
func (c *Config) Servers() map[string]ResolvedServer {
	out := map[string]ResolvedServer{}
	add := func(layer string, servers map[string]Server) {
		for alias, s := range servers {
			r := out[alias]
			r.Alias = alias
			if s.URL != "" {
				r.URL = s.URL
			}
			// auth_env is applied only from the machine layer, and only
			// when that same machine entry also sets url. A project-layer
			// auth_env must never reach ResolvedServer -- load.go's
			// checkDecodedProjectAuthEnv already hard-errors on one, but
			// this is defence in depth so a future path that skips
			// validation still can't act on it. And a bare machine
			// "servers.X: {auth_env}" with no url of its own must not end
			// up paired with a url written by a different layer -- such as
			// a project file -- sending that env var's secret to a host
			// its owner never approved (controller ruling F-R1).
			if layer == "machine" && s.AuthEnv != "" && s.URL != "" {
				r.AuthEnv = s.AuthEnv
			}
			if len(s.Projects) > 0 {
				r.Projects = s.Projects
			}
			if s.DefaultProject != "" {
				r.DefaultProject = s.DefaultProject
			}
			r.DefinedIn = append(r.DefinedIn, layer)
			out[alias] = r
		}
	}
	if c.Project != nil {
		add("project", c.Project.Servers)
	}
	add("machine", c.Machine.Servers)
	if c.Env.URL != "" {
		out[EnvServerAlias] = ResolvedServer{Alias: EnvServerAlias, URL: c.Env.URL, Token: c.Env.Token, DefinedIn: []string{"env"}}
	}
	return out
}

// RepoEntry returns the machine file's repos entry for this repository.
func (c *Config) RepoEntry() (Repo, bool) {
	_, r, ok := c.repoEntry()
	return r, ok
}

// OtherRepoTargets returns, by repos key as written, the target names that
// machine repos entries for other repositories define. Those targets do not
// apply here.
func (c *Config) OtherRepoTargets() map[string][]string {
	here, _, _ := c.repoEntry()
	out := map[string][]string{}
	for _, key := range sortedKeys(c.Machine.Repos) {
		if key != here && len(c.Machine.Repos[key].Targets) > 0 {
			out[key] = sortedKeys(c.Machine.Repos[key].Targets)
		}
	}
	return out
}

// repoEntry also returns the repos key as written, e.g. "~/src/repo".
func (c *Config) repoEntry() (string, Repo, bool) {
	for key, r := range c.Machine.Repos {
		if c.expand(key) == filepath.Clean(c.RepoRoot) {
			return key, r, true
		}
	}
	return "", Repo{}, false
}

func (c *Config) expand(path string) string {
	if strings.HasPrefix(path, "~/") && c.Home != "" {
		path = filepath.Join(c.Home, path[2:])
	}
	return filepath.Clean(path)
}

// UnknownServer is the error for an alias no layer defines with a url. It
// lists the aliases that do, so a mistyped --server is easy to spot.
func (c *Config) UnknownServer(alias string) *errs.Error {
	e := errs.Configf("no server alias %q", alias).
		WithTry(fmt.Sprintf("bam server add %s --url URL", alias))
	var names []string
	for n, s := range c.Servers() {
		if s.URL != "" {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		slices.Sort(names)
		e = e.WithWhy("servers: " + strings.Join(names, ", "))
	}
	return e
}

// SelectServer applies spec §3.5: flag, BAM_SERVER, BAM_URL, target server,
// machine repos entry, project default, machine default, the only server.
func (c *Config) SelectServer(flag, targetServer string) (ResolvedServer, error) {
	servers := c.Servers()
	pick := func(alias string) (ResolvedServer, error) {
		s, ok := servers[alias]
		if !ok || s.URL == "" {
			return ResolvedServer{}, c.UnknownServer(alias)
		}
		return s, nil
	}
	repo, _ := c.RepoEntry()
	projectDefault := ""
	if c.Project != nil {
		projectDefault = c.Project.DefaultServer
	}
	switch {
	case flag != "":
		return pick(flag)
	case c.Env.Server != "":
		return pick(c.Env.Server)
	case c.Env.URL != "":
		return pick(EnvServerAlias)
	case targetServer != "":
		return pick(targetServer)
	case repo.Server != "":
		return pick(repo.Server)
	case projectDefault != "":
		return pick(projectDefault)
	case c.Machine.DefaultServer != "":
		return pick(c.Machine.DefaultServer)
	}
	aliases := sortedKeys(servers)
	switch len(aliases) {
	case 1:
		return servers[aliases[0]], nil
	case 0:
		return ResolvedServer{}, errs.Configf("no Bamboo server configured").
			WithTry("bam server add <alias> --url URL")
	}
	return ResolvedServer{}, errs.Configf("no server selected").
		WithWhy("configured aliases: " + strings.Join(aliases, ", ")).
		WithTry("pass --server ALIAS, or set default_server in .bam.yaml")
}

// ProjectKeys returns the projects that scope navigation for server s.
func (c *Config) ProjectKeys(s ResolvedServer) []string {
	if repo, ok := c.RepoEntry(); ok && len(repo.Projects) > 0 {
		return repo.Projects
	}
	if c.Project != nil && len(c.Project.Projects) > 0 {
		return c.Project.Projects
	}
	return s.Projects
}

// DefaultProject returns the project the terminal UI opens on for server s,
// uppercased, or "" for every project. It is looked up in the same order as
// ProjectKeys: the repos entry, the project file, the server entry.
func (c *Config) DefaultProject(s ResolvedServer) string {
	if repo, ok := c.RepoEntry(); ok && repo.DefaultProject != "" {
		return strings.ToUpper(repo.DefaultProject)
	}
	if c.Project != nil && c.Project.DefaultProject != "" {
		return strings.ToUpper(c.Project.DefaultProject)
	}
	return strings.ToUpper(s.DefaultProject)
}

// ResolvedTarget is a target after merging every layer that defines it.
type ResolvedTarget struct {
	Name string
	Target
	DefinedIn []string      // file paths, lowest precedence first
	Layers    []TargetLayer // the raw entry of each defining layer, lowest precedence first
}

// TargetLayer is one layer's own entry for a target and where it lives.
type TargetLayer struct {
	Path    string   // file on disk
	KeyPath []string // mapping path to the targets map, e.g. ["targets"] or ["repos", key, "targets"]
	Target  Target
}

// Targets merges targets by name: project, then machine top-level, then the
// presets file and directory, then the machine repos entry for this
// repository. It validates the merged result.
func (c *Config) Targets() (map[string]ResolvedTarget, error) {
	out := map[string]ResolvedTarget{}
	layer := func(label, path string, keyPath []string, targets map[string]Target) {
		for name, t := range targets {
			r := out[name]
			r.Name = name
			r.Target = mergeTarget(r.Target, t)
			r.DefinedIn = append(r.DefinedIn, label)
			r.Layers = append(r.Layers, TargetLayer{Path: path, KeyPath: keyPath, Target: t})
			out[name] = r
		}
	}
	if c.Project != nil {
		layer(c.ProjectPath, c.ProjectPath, []string{"targets"}, c.Project.Targets)
	}
	layer(c.MachinePath, c.MachinePath, []string{"targets"}, c.Machine.Targets)
	for _, s := range c.presetsSources() {
		layer(s.Path, s.Path, []string{"presets"}, s.Presets)
	}
	if key, repo, ok := c.repoEntry(); ok {
		layer(c.MachinePath+" (repos)", c.MachinePath, []string{"repos", key, "targets"}, repo.Targets)
	}
	servers := c.Servers()
	for _, name := range sortedKeys(out) {
		if err := validateTarget(out[name], servers); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Target returns one merged target.
func (c *Config) Target(name string) (ResolvedTarget, bool, error) {
	all, err := c.Targets()
	if err != nil {
		return ResolvedTarget{}, false, err
	}
	t, ok := all[name]
	return t, ok, nil
}

func mergeTarget(base, over Target) Target {
	if over.Plan != "" {
		base.Plan = over.Plan
	}
	if over.Server != "" {
		base.Server = over.Server
	}
	if over.Branch != "" {
		base.Branch = over.Branch
	}
	if len(over.Defaults) > 0 {
		merged := StringMap{}
		for k, v := range base.Defaults {
			merged[k] = v
		}
		for k, v := range over.Defaults {
			merged[k] = v
		}
		base.Defaults = merged
	}
	if len(over.Options) > 0 {
		merged := StringListMap{}
		for k, v := range base.Options {
			merged[k] = v
		}
		for k, v := range over.Options {
			merged[k] = v
		}
		base.Options = merged
	}
	if over.Required != nil {
		base.Required = over.Required
	}
	if over.Watch != nil {
		base.Watch = over.Watch
	}
	if over.Timeout != 0 {
		base.Timeout = over.Timeout
	}
	return base
}

func validateTarget(t ResolvedTarget, servers map[string]ResolvedServer) error {
	where := strings.Join(t.DefinedIn, ", ")
	if t.Plan == "" {
		return errs.Configf("preset %q has no plan", t.Name).
			WithWhy("defined in " + where).
			WithTry(fmt.Sprintf("add plan: PROJ-PLAN under targets.%s", t.Name))
	}
	if !childPlanKeyRe.MatchString(t.Plan) {
		try := fmt.Sprintf("set plan: PROJ-PLAN under targets.%s", t.Name)
		if up := strings.ToUpper(t.Plan); childPlanKeyRe.MatchString(up) {
			try = "plan keys are uppercase: did you mean " + up + "?"
		}
		return errs.Configf("preset %q: plan %q is not a plan key", t.Name, t.Plan).
			WithWhy("defined in " + where).
			WithTry(try)
	}
	if t.Server != "" {
		if _, ok := servers[t.Server]; !ok {
			return errs.Configf("preset %q uses unknown server %q", t.Name, t.Server).
				WithTry(fmt.Sprintf("bam server add %s --url URL", t.Server))
		}
	}
	for _, name := range sortedKeys(t.Options) {
		val, ok := t.Defaults[name]
		if !ok || val == "" {
			continue
		}
		if _, isRef := EnvRef(val); isRef {
			continue
		}
		if !slices.Contains(t.Options[name], val) {
			display := val
			if IsMaskedName(name) {
				display = "********"
			}
			return errs.Configf("preset %q: default %s=%q is not one of %s", t.Name, name, display, strings.Join(t.Options[name], ", ")).
				WithWhy("defined in " + where)
		}
	}
	return nil
}
