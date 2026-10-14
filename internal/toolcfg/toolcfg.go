// Package toolcfg gives the developer tools -- the fixture recorder and the
// e2e suite -- the same configuration bam itself uses: server aliases from
// ~/.config/bam/config.yaml and .bam.yaml, tokens from the keychain or the
// credentials file. A tool names an alias or a target; it never asks anyone
// to export a URL and a token.
package toolcfg

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adrg/xdg"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// Options say where the configuration and the credentials live. Tests fill
// every field; tools use SystemOptions.
type Options struct {
	WorkDir     string
	Home        string
	MachineFile string
	Credentials string
	Getenv      func(string) string
	Keyring     credential.Keyring
}

// SystemOptions are the paths bam itself uses for the running process.
func SystemOptions() (Options, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Options{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Options{}, err
	}
	machine := os.Getenv("BAM_CONFIG")
	if machine == "" {
		machine = filepath.Join(xdg.ConfigHome, "bam", "config.yaml")
	}
	return Options{
		WorkDir:     wd,
		Home:        home,
		MachineFile: machine,
		Credentials: filepath.Join(xdg.ConfigHome, "bam", "credentials.yaml"),
		Getenv:      os.Getenv,
		Keyring:     credential.SystemKeyring{},
	}, nil
}

// Server is one resolved server: where it is and how to authenticate.
type Server struct {
	Alias  string
	URL    string // no trailing slash
	Origin string
	Token  string
}

// Loaded is the configuration of one working directory.
type Loaded struct {
	Cfg   *config.Config
	store *credential.Store
}

// Load reads both configuration layers.
func Load(o Options) (*Loaded, error) {
	cfg, err := config.Load(config.LoadOptions{
		WorkDir:     o.WorkDir,
		Home:        o.Home,
		MachineFile: o.MachineFile,
		Getenv:      o.Getenv,
	})
	if err != nil {
		return nil, err
	}
	return &Loaded{
		Cfg:   cfg,
		store: &credential.Store{Keyring: o.Keyring, FilePath: o.Credentials, Getenv: o.Getenv},
	}, nil
}

// Server selects a server the way bam does (--server, then the defaults of
// this repository and machine) and finds its token. alias may be empty.
func (l *Loaded) Server(alias string) (Server, error) {
	return l.ServerFor(alias, "")
}

// ServerFor is Server with the server a target names, which ranks below an
// explicit alias, exactly as in bam's own selection order.
func (l *Loaded) ServerFor(alias, targetServer string) (Server, error) {
	rs, err := l.Cfg.SelectServer(alias, targetServer)
	if err != nil {
		return Server{}, err
	}
	url := strings.TrimRight(rs.URL, "/")
	origin, err := credential.Origin(url)
	if err != nil {
		return Server{}, err
	}
	token := rs.Token // only set for the BAM_URL server
	if token == "" {
		token, _, err = l.store.Lookup(rs.Alias, origin, rs.AuthEnv)
		if err != nil {
			return Server{}, err
		}
	}
	return Server{Alias: rs.Alias, URL: url, Origin: origin, Token: token}, nil
}

// PlanFor returns the plan key of a target, and the server alias the target
// names (empty when it names none).
func (l *Loaded) PlanFor(name string) (plan, server string, err error) {
	t, ok, err := l.Cfg.Target(name)
	if err != nil {
		return "", "", err
	}
	if !ok {
		e := errs.Configf("no preset %q", name).WithTry("bam preset list")
		if names := l.targetNames(); len(names) > 0 {
			e = e.WithWhy("presets here: " + strings.Join(names, ", "))
		}
		return "", "", e
	}
	return t.Plan, t.Server, nil
}

func (l *Loaded) targetNames() []string {
	all, err := l.Cfg.Targets()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
