package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/spf13/cobra"
)

type globalFlags struct {
	server string
	color  string
	json   bool
	debug  bool
	noTUI  bool
	// project is the terminal UI's --project, on bam and bam ui only.
	project string
}

// runtime holds what commands share: the Env, global flags and the lazily
// loaded configuration.
type runtime struct {
	env   Env
	flags globalFlags
	cfg   *config.Config
}

// resultError reports a watched build that did not succeed. It maps to exit
// code 1 and prints nothing more; the result is already on screen.
type resultError struct{ State provider.State }

func (e *resultError) Error() string { return "build finished: " + string(e.State) }

func (r *runtime) config() (*config.Config, error) {
	if r.cfg != nil {
		return r.cfg, nil
	}
	cfg, err := r.loadConfig()
	if err != nil {
		return nil, err
	}
	r.cfg = cfg
	return cfg, nil
}

// loadConfig reads the configuration files afresh, bypassing the cache. The
// terminal UI uses it so refreshing its Presets panel shows an edit made to
// .bam.yaml while the UI is open.
func (r *runtime) loadConfig() (*config.Config, error) {
	return config.Load(config.LoadOptions{WorkDir: r.env.WorkDir, Home: r.env.Home, MachineFile: r.env.Paths.MachineConfig, Getenv: r.env.Getenv})
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

func (r *runtime) store() *credential.Store {
	return &credential.Store{Keyring: r.env.Keyring, FilePath: r.env.Paths.Credentials, Getenv: r.env.Getenv}
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
		token, _, err = r.store().Lookup(server.Alias, origin, server.AuthEnv)
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
	svc := &app.Service{P: backend, Cfg: r.cfg, Server: server, Origin: origin, Clock: r.env.Clock,
		State: &app.StateStore{Path: r.env.Paths.State}, Sets: r.setStore(), Getenv: r.env.Getenv}
	return svc, backend, nil
}

// connectFor connects to the server of a target argument, or the default one.
func (r *runtime) connectFor(ctx context.Context, arg string) (*app.Service, Backend, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, nil, err
	}
	targetServer := ""
	if config.ValidTargetName(arg) {
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
	for _, s := range cfg.Servers() {
		if o, err := credential.Origin(s.URL); err == nil && o == rec.Origin {
			return r.connectServer(s)
		}
	}
	return nil, nil, errs.Configf("the last build %s ran on %s, which no configured server matches", rec.BuildKey, rec.Origin).
		WithTry("bam server add <alias> --url " + rec.Origin)
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
