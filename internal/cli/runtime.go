package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/credential"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/bamboo"
	"github.com/rojcarranza/bam/internal/provider/readonly"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/rojcarranza/bam/internal/view/style"
	"github.com/spf13/cobra"
)

type globalFlags struct {
	server string
	color  string
	json   bool
	debug  bool
	noTUI  bool
	// readOnly is --read-only: refuse every command that changes Bamboo.
	readOnly bool
	// project is the terminal UI's --project, on bam and bam ui only.
	project string
}

// runtime holds what commands share: the Env, global flags and the lazily
// loaded configuration.
type runtime struct {
	env   Env
	flags globalFlags
	// src is the configuration source, made on first use; cfg is the first one
	// it returned, for commands that read it after config() has run.
	src   *config.Source
	cfg   *config.Config
	trees *app.TreeStore
	// notify is set by --notify on commands that watch a build.
	notify bool
}

// treeStore is one for the process, so every server's Service in the
// terminal UI shares what it has read. With no directory it keeps none.
func (r *runtime) treeStore() *app.TreeStore {
	if r.trees == nil && r.env.Paths.Trees != "" {
		r.trees = &app.TreeStore{Dir: r.env.Paths.Trees}
	}
	return r.trees
}

// resultError reports a watched build that did not succeed. It maps to exit
// code 1 and prints nothing more; the result is already on screen.
type resultError struct{ State provider.State }

func (e *resultError) Error() string { return "build finished: " + string(e.State) }

// source is the process's one configuration source, made on first use.
func (r *runtime) source() (*config.Source, error) {
	if r.src != nil {
		return r.src, nil
	}
	src, err := config.NewSource(r.loadOptions)
	if err != nil {
		return nil, err
	}
	src.MinInterval = time.Second
	r.src = src
	return src, nil
}

// config is the current configuration. It reloads when a file changed since
// the last call, so the terminal UI's panels and its service see an edit made
// while it is open, and a preset or pin bam wrote itself.
func (r *runtime) config() (*config.Config, error) {
	cfg, err := r.current()
	if err != nil {
		return nil, err
	}
	// r.cfg is set once: the terminal UI calls config from several goroutines,
	// and a later write would race with the reads of commands.
	if r.cfg == nil {
		r.cfg = cfg
	}
	return cfg, nil
}

// current is config without remembering the result in r.cfg. The terminal
// UI's callbacks run on several goroutines and use this one, so nothing of
// the runtime is written from them.
func (r *runtime) current() (*config.Config, error) {
	src, err := r.source()
	if err != nil {
		return nil, err
	}
	return src.Current()
}

// configChanged tells the source that bam itself wrote a configuration file,
// so the next read reloads even when the file's stamp did not move.
func (r *runtime) configChanged() {
	if r.src != nil {
		r.src.MarkStale()
	}
}

func (r *runtime) loadOptions() config.LoadOptions {
	return config.LoadOptions{WorkDir: r.env.WorkDir, Home: r.env.Home, MachineFile: r.env.Paths.MachineConfig,
		PresetsFile: r.presets().Path, Getenv: r.env.Getenv}
}

func (r *runtime) out() (view.Out, error) {
	flag := r.flags.color
	if flag == "" {
		if cfg, err := r.config(); err == nil {
			flag = cfg.Machine.Color
		}
	}
	color, err := style.ColorEnabled(flag, r.env.Getenv("NO_COLOR"), r.env.StdoutTTY)
	if err != nil {
		return view.Out{}, err
	}
	links := r.env.StdoutTTY && r.env.Getenv("TERM") != "dumb"
	return view.Out{W: r.env.Stdout, TTY: r.env.StdoutTTY, Width: r.env.Width(), Style: style.Mode{Color: color, Links: links}, Now: r.env.Clock.Now}, nil
}

// show prints a command's result: doc as JSON with --json, otherwise text
// rendered to the styled stdout.
func (r *runtime) show(doc any, text func(view.Out) error) error {
	if r.flags.json {
		return view.WriteJSON(r.env.Stdout, doc)
	}
	o, err := r.out()
	if err != nil {
		return err
	}
	return text(o)
}

func (r *runtime) store() *credential.Store {
	return &credential.Store{Keyring: r.env.Keyring, FilePath: r.env.Paths.Credentials, Getenv: r.env.Getenv}
}

// lookupToken finds the stored token for server, and says why BAM_TOKEN did
// not count when it is set: it applies only to the BAM_URL server.
func (r *runtime) lookupToken(server config.ResolvedServer, origin string) (string, credential.Source, error) {
	token, src, err := r.store().Lookup(server.Alias, origin, server.AuthEnv)
	var e *errs.Error
	if err != nil && server.Alias == config.EnvServerAlias && errors.As(err, &e) && e.Kind == errs.KindAuth {
		// BAM_URL with no BAM_TOKEN: the env var is the expected way to give its token.
		e.Why = "BAM_TOKEN is not set and nothing is stored"
		e.Try = "export BAM_TOKEN=<personal access token>, or bam login " + config.EnvServerAlias
	} else if err != nil && r.env.Getenv("BAM_TOKEN") != "" && errors.As(err, &e) && e.Kind == errs.KindAuth {
		why := "BAM_TOKEN is set, but it applies only to the server named by BAM_URL"
		if e.Why != "" {
			why = e.Why + "; " + why
		}
		e.Why = why
	}
	return token, src, err
}

func (r *runtime) logger() *slog.Logger {
	if !r.flags.debug {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return slog.New(slog.NewTextHandler(r.env.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// connect selects the server (spec §3.5), finds its token and builds a Service.
func (r *runtime) connect(ctx context.Context, targetServer string) (*app.Service, Backend, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, nil, err
	}
	server, err := cfg.SelectServer(r.flags.server, targetServer)
	if err != nil {
		return nil, nil, err
	}
	return r.connectServer(server)
}

func (r *runtime) connectServer(server config.ResolvedServer) (*app.Service, Backend, error) {
	origin, err := credential.Origin(server.URL)
	if err != nil {
		return nil, nil, err
	}
	token := server.Token
	if token == "" {
		token, _, err = r.lookupToken(server, origin)
		if err != nil {
			return nil, nil, err
		}
	}
	caps, _ := bamboo.LoadCapabilities(r.env.Paths.Capabilities, origin)
	backend, err := r.env.Connect(bamboo.Options{
		BaseURL: server.URL,
		Token:   token,
		Logger:  r.logger(),
		Caps:    caps,
		SaveCaps: func(c bamboo.Capabilities) {
			_ = bamboo.SaveCapabilities(r.env.Paths.Capabilities, origin, c)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	ro, err := r.isReadOnly()
	if err != nil {
		return nil, nil, err
	}
	if ro {
		backend = readOnlyBackend{Guard: readonly.Wrap(backend), probe: backend}
	}
	src, err := r.source()
	if err != nil {
		return nil, nil, err
	}
	svc := &app.Service{P: backend, ReadOnly: ro, Cfg: src, Server: server, Origin: origin, Clock: r.env.Clock,
		State: &app.StateStore{Path: r.env.Paths.State}, Sets: r.setStore(), Trees: r.treeStore(), Getenv: r.env.Getenv,
		Checkout: r.checkedOutBranch, Head: r.checkedOutCommit}
	if r.env.Paths.AgentJobs != "" {
		svc.AgentJobs = &app.AgentJobStore{Dir: r.env.Paths.AgentJobs}
	}
	if r.env.Paths.JobAgents != "" {
		svc.JobAgents = &app.JobAgentStore{Dir: r.env.Paths.JobAgents}
	}
	return svc, backend, nil
}

// connectFor connects to the server of a target argument, or the default one.
func (r *runtime) connectFor(ctx context.Context, arg string) (*app.Service, Backend, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, nil, err
	}
	targetServer := ""
	if arg = app.PresetName(cfg, arg); config.ValidTargetName(arg) {
		if t, ok, err := cfg.Target(arg); err != nil {
			return nil, nil, err
		} else if ok {
			targetServer = t.Server
		}
	}
	return r.connect(ctx, targetServer)
}

// connectLast connects to the server where this repository's last build ran.
func (r *runtime) connectLast(ctx context.Context) (*app.Service, Backend, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, nil, err
	}
	rec, ok, err := (&app.StateStore{Path: r.env.Paths.State}).Last(cfg.RepoRoot)
	if err != nil {
		return nil, nil, err
	}
	if !ok || r.flags.server != "" {
		return r.connect(ctx, "")
	}
	if s, ok := serverForOrigin(cfg, rec.Origin); ok {
		return r.connectServer(s)
	}
	return nil, nil, errs.Configf("the last build %s ran on %s, which no configured server matches", rec.BuildKey, rec.Origin).
		WithTry("bam server add <alias> --url " + rec.Origin)
}

// serverForOrigin returns the configured server at origin. When several
// aliases share it, the first alias in sorted order wins.
func serverForOrigin(cfg *config.Config, origin string) (config.ResolvedServer, bool) {
	servers := cfg.Servers()
	for _, alias := range slices.Sorted(maps.Keys(servers)) {
		if o, err := credential.Origin(servers[alias].URL); err == nil && o == origin {
			return servers[alias], true
		}
	}
	return config.ResolvedServer{}, false
}

// wrap classifies stray errors from a command as internal so that only
// cobra's own argument errors are left unclassified (exit 2).
func (r *runtime) wrap(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := fn(cmd, args)
		if err == nil {
			return nil
		}
		var e *errs.Error
		var res *resultError
		if errors.As(err, &e) || errors.As(err, &res) || errors.As(err, new(silentError)) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errs.New(errs.KindInternal, err.Error()).Wrap(err)
	}
}

// silentError carries an exit code for an error the command already printed.
type silentError struct{ error }

func (s silentError) Unwrap() error { return s.error }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var res *resultError
	if errors.As(err, &res) {
		return 1
	}
	// *errs.Error is checked before the context sentinels: an http.Client
	// timeout comes back as a *errs.Error of KindBamboo whose cause chain
	// happens to satisfy errors.Is(err, context.DeadlineExceeded), and that
	// must still exit 5, not 6 (6 is reserved for --timeout/KindTimeout).
	var e *errs.Error
	if errors.As(err, &e) {
		switch e.Kind {
		case errs.KindUsage:
			return 2
		case errs.KindConfig:
			return 3
		case errs.KindAuth:
			return 4
		case errs.KindTimeout:
			return 6
		default:
			return 5
		}
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 6
	}
	return 2 // cobra's own command, flag and argument errors
}

// presets locates the personal presets file.
func (r *runtime) presets() config.PresetsLocation {
	return config.LocatePresets(config.PresetsLookup{Home: r.env.Home, Getenv: r.env.Getenv, Glob: r.env.Glob})
}

// presetsWritePath is where preset add writes. In WSL, with no presets file
// anywhere yet, it creates one in the Windows home so that bam.exe in Git
// Bash reads the same file.
func (r *runtime) presetsWritePath() string {
	loc := r.presets()
	if loc.Exists || loc.Note != "" || r.env.Getenv(config.PresetsEnv) != "" || !config.IsWSL(r.env.Getenv) || r.env.WindowsHome == nil {
		return loc.Path
	}
	win, err := r.env.WindowsHome()
	if err != nil || win == "" {
		return loc.Path
	}
	return config.DefaultPresetsPath(win)
}

// isReadOnly says whether bam must refuse every command that changes Bamboo:
// --read-only, BAM_READ_ONLY set to a true value, or read_only: true in the
// machine config. Any one turns it on; none of them turns off another.
func (r *runtime) isReadOnly() (bool, error) {
	if r.flags.readOnly {
		return true, nil
	}
	if v := r.env.Getenv("BAM_READ_ONLY"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return false, errs.Usagef("BAM_READ_ONLY is %q, which is not true or false", v).
				WithTry("set it to 1 or true, or unset it")
		}
		if on {
			return true, nil
		}
	}
	cfg, err := r.config()
	if err != nil {
		return false, err
	}
	return cfg.Machine.ReadOnly, nil
}

// readOnlyBackend is a Backend whose writes are refused. Doctor's probe only
// reads, so it goes to the real backend.
type readOnlyBackend struct {
	*readonly.Guard
	probe Backend
}

func (b readOnlyBackend) Probe(ctx context.Context, planKey string) []bamboo.ProbeResult {
	return b.probe.Probe(ctx, planKey)
}

// refuseWrite stops a command that is about to change something in Bamboo when
// bam is read-only, before it prompts, takes a lock or writes a record. The
// wrapped backend refuses the same calls, so a command that forgets to ask
// still cannot reach the server.
func (r *runtime) refuseWrite(what string) error {
	ro, err := r.isReadOnly()
	if err != nil {
		return err
	}
	if ro {
		return readonly.Refusal(what)
	}
	return nil
}
