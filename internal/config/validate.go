package config

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"gopkg.in/yaml.v3"
)

var (
	targetNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	envRefRe     = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
	nonAlnumRe   = regexp.MustCompile(`[^A-Za-z0-9]+`)

	// Bamboo's default list of variable-name fragments it masks.
	maskedFragments = []string{"password", "secret", "passphrase", "sshkey"}

	// Keys that must never appear under servers.<alias> in a committed file.
	credentialKeys = map[string]bool{"token": true, "password": true, "pat": true, "secret": true}
)

// EnvRef reports whether v is exactly ${NAME} and returns NAME.
func EnvRef(v string) (string, bool) {
	m := envRefRe.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// IsMaskedName reports whether Bamboo treats a variable with this name as secret.
func IsMaskedName(name string) bool {
	lower := strings.ToLower(name)
	for _, f := range maskedFragments {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

// EnvNameFor suggests an environment variable name for a Bamboo variable.
func EnvNameFor(variable string) string {
	return strings.Trim(strings.ToUpper(nonAlnumRe.ReplaceAllString(variable, "_")), "_")
}

// ValidTargetName reports whether name can be a target name. Target names are
// lowercase so they never collide with uppercase plan keys.
func ValidTargetName(name string) bool { return targetNameRe.MatchString(name) }

// TargetNameFor suggests a valid target or set name for name, or returns ""
// when lowercasing and dashing it still does not make one.
func TargetNameFor(name string) string {
	s := strings.Trim(strings.ToLower(nonAlnumRe.ReplaceAllString(name, "-")), "-")
	if !ValidTargetName(s) {
		return ""
	}
	return s
}

// renameTry is the try line for an invalid target or set name in a file.
func renameTry(name string) string {
	if s := TargetNameFor(name); s != "" {
		return fmt.Sprintf("rename it, for example %q", s)
	}
	return "rename it to start with a lowercase letter, then letters, digits, - or _"
}

// checkProjectCredentialKeys fails when a committed file carries a credential.
// It runs on the raw YAML so it reports the key even though strict decoding
// would also reject it.
func checkProjectCredentialKeys(data []byte, path string) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return nil // decodeStrict reports syntax errors
	}
	servers := mappingValue(root.Content[0], "servers")
	if servers == nil || servers.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(servers.Content); i += 2 {
		alias, body := servers.Content[i].Value, servers.Content[i+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(body.Content); j += 2 {
			key := body.Content[j].Value
			lower := strings.ToLower(key)
			if lower == "auth_env" {
				return authEnvNotAllowedError(path, alias)
			}
			if credentialKeys[lower] {
				return errs.Configf("credential found in %s at servers.%s.%s", path, alias, key).
					WithWhy("tokens never go in a committed file").
					WithTry(fmt.Sprintf("remove the line, revoke that token in Bamboo, then run: bam login %s", alias))
			}
		}
	}
	return nil
}

// authEnvNotAllowedError is the error returned whenever a committed project
// file sets servers.<alias>.auth_env, whether the raw-YAML scan spotted it
// literally or the decoded struct caught it after a merge key, anchor or
// alias hid it from that scan.
func authEnvNotAllowedError(path, alias string) error {
	return errs.Configf("%s: servers.%s.auth_env is not allowed in a committed file", path, alias).
		WithWhy("auth_env names a secret environment variable; only the machine config may set it").
		WithTry("move auth_env to ~/.config/bam/config.yaml")
}

// checkDecodedProjectAuthEnv rejects servers.<alias>.auth_env on the decoded
// ProjectFile struct. Unlike checkProjectCredentialKeys (which scans the raw
// YAML node tree and can be evaded by a merge key or an anchor/alias that
// hides the auth_env pair from that walk), decoding through yaml.v3 already
// resolves merge keys, anchors and aliases, so a populated AuthEnv field here
// is real regardless of how the YAML expressed it.
func checkDecodedProjectAuthEnv(pf *ProjectFile, path string) error {
	for _, alias := range sortedKeys(pf.Servers) {
		if pf.Servers[alias].AuthEnv != "" {
			return authEnvNotAllowedError(path, alias)
		}
	}
	return nil
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// validateFiles applies the rules that need only one file at a time.
func validateFiles(c *Config) error {
	if c.Project != nil {
		if err := checkReservedAliases(c.ProjectPath, c.Project.Servers); err != nil {
			return err
		}
		if err := checkTargetNames(c.ProjectPath, "targets", c.Project.Targets); err != nil {
			return err
		}
		if err := checkSetNames(c.ProjectPath, "sets", c.Project.Sets); err != nil {
			return err
		}
		if err := checkSetSecrets(c.ProjectPath, c.Project.Sets); err != nil {
			return err
		}
		for _, name := range sortedKeys(c.Project.Targets) {
			t := c.Project.Targets[name]
			for _, v := range sortedKeys(t.Defaults) {
				val := t.Defaults[v]
				if _, ok := EnvRef(val); ok || val == "" || !IsMaskedName(v) {
					continue
				}
				return errs.Configf("%s: targets.%s.defaults.%s holds a literal secret", c.ProjectPath, name, v).
					WithWhy("variables named like password, secret, passphrase or sshkey cannot be committed").
					WithTry(fmt.Sprintf(`use an environment reference: %s: "${%s}"`, v, EnvNameFor(v)))
			}
		}
	}
	switch strings.ToLower(c.Machine.Color) {
	case "", "auto", "always", "never":
	default:
		return errs.Configf("%s: color must be auto, always or never, not %q", c.MachinePath, c.Machine.Color).
			WithTry("set color: to auto, always or never, or remove the line")
	}
	if err := checkReservedAliases(c.MachinePath, c.Machine.Servers); err != nil {
		return err
	}
	if err := checkTargetNames(c.MachinePath, "targets", c.Machine.Targets); err != nil {
		return err
	}
	if err := checkSetNames(c.MachinePath, "sets", c.Machine.Sets); err != nil {
		return err
	}
	for _, s := range c.presetsSources() {
		if err := checkTargetNames(s.Path, "presets", s.Presets); err != nil {
			return err
		}
	}
	for _, root := range sortedKeys(c.Machine.Repos) {
		if err := checkTargetNames(c.MachinePath, "repos."+root+".targets", c.Machine.Repos[root].Targets); err != nil {
			return err
		}
		if err := checkSetNames(c.MachinePath, "repos."+root+".sets", c.Machine.Repos[root].Sets); err != nil {
			return err
		}
	}
	return nil
}

func checkTargetNames(path, where string, targets map[string]Target) error {
	for _, name := range sortedKeys(targets) {
		if !ValidTargetName(name) {
			return errs.Configf("%s: %s: invalid preset name %q", path, where, name).
				WithWhy("preset names are lowercase letters, digits, - and _, starting with a letter").
				WithTry(renameTry(name))
		}
	}
	return nil
}

func checkSetNames(path, where string, sets map[string]Set) error {
	for _, name := range sortedKeys(sets) {
		if !ValidTargetName(name) {
			return errs.Configf("%s: %s: invalid set name %q", path, where, name).
				WithWhy("set names are lowercase letters, digits, - and _, starting with a letter").
				WithTry(renameTry(name))
		}
	}
	return nil
}

// checkSetSecrets refuses a literal secret in a committed set's vars, as
// target defaults do.
func checkSetSecrets(path string, sets map[string]Set) error {
	for _, name := range sortedKeys(sets) {
		for i, st := range sets[name].Stages {
			for _, r := range st {
				// Every offending name is collected and the first in sorted
				// order reported, so the message is the same on every run.
				var bad []string
				r.values(func(name, val string) {
					if _, ok := EnvRef(val); ok || val == "" || !IsMaskedName(name) {
						return
					}
					bad = append(bad, name)
				})
				if len(bad) > 0 {
					slices.Sort(bad)
					badName := bad[0]
					return errs.Configf("%s: sets.%s stage %d %s: vars.%s holds a literal secret", path, name, i+1, r.Target, badName).
						WithWhy("variables named like password, secret, passphrase or sshkey cannot be committed").
						WithTry(fmt.Sprintf(`use an environment reference: %s: "${%s}"`, badName, EnvNameFor(badName)))
				}
			}
		}
	}
	return nil
}

func checkReservedAliases(path string, servers map[string]Server) error {
	if _, ok := servers[EnvServerAlias]; ok {
		return errs.Configf("%s: server alias %q is reserved", path, EnvServerAlias).
			WithWhy(EnvServerAlias + " is the server defined by BAM_URL").
			WithTry("rename the alias, for example \"env-server\"")
	}
	return nil
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
