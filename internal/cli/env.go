package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/pkg/browser"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/credential"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/bamboo"
	"github.com/rojcarranza/bam/internal/view/tui"
	"golang.org/x/term"
)

// Backend is a server connection: the neutral Provider plus doctor's probe.
type Backend interface {
	provider.Provider
	Probe(ctx context.Context, planKey string) []bamboo.ProbeResult
}

// Paths are bam's files outside the repository.
type Paths struct {
	MachineConfig string
	Credentials   string
	Capabilities  string
	State         string
	Usage         string // the private tally of commands run, one small file
	Sets          string // directory of set run records and locks
	Trees         string // directory of saved build trees, one file per server
	AgentJobs     string // directory of the jobs each agent can run, one file per server
	JobAgents     string // directory of which agent ran each finished job, one file per server
	// LegacyConfigDir is where bam kept its config before it used
	// ~/.config/bam on every platform; Execute moves files from it. Empty
	// means nothing to move.
	LegacyConfigDir string
}

// Env is everything the CLI takes from the process. Tests build it by hand.
type Env struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Getenv    func(string) string
	StdoutTTY bool
	StderrTTY bool
	StdinTTY  bool
	Width     func() int
	WorkDir   string
	// GitBranch returns the branch checked out in dir; nil means bam cannot
	// tell, and "@" is refused.
	GitBranch func(dir string) (string, error)
	// GitHead returns the commit checked out in dir and whether a remote
	// branch contains it; nil means bam cannot tell.
	GitHead     func(dir string) (sha string, pushed bool, err error)
	Home        string
	Paths       Paths
	Clock       app.Clock
	Keyring     credential.Keyring
	Connect     func(bamboo.Options) (Backend, error)
	OpenBrowser func(url string) error
	// Notify raises a desktop notification; nil, or an error, means this
	// system has none and only the terminal bell rings.
	Notify     func(title, body string) error
	RunPager   func(cmd string, r io.Reader) error
	RunEditor  func(cmd string, initial []byte) ([]byte, error)
	ReadSecret func() (string, error)
	// RunTUI opens the terminal UI. It is a field so tests can assert on the
	// Deps the CLI builds without starting a terminal program.
	RunTUI func(context.Context, tui.Deps) error
	// PickPreset is the picker of bare bam run; a field so tests can script
	// the choice without a terminal.
	PickPreset func(ctx context.Context, in io.Reader, out io.Writer, items []app.TargetInfo, words tui.PickerWords) (string, error)
	// Choose, Ask and EditPreset are the inline prompts of bam preset add on
	// a terminal; fields so tests can script the answers without one.
	Choose     func(ctx context.Context, in io.Reader, out io.Writer, prompt string, items []tui.Choice) (int, error)
	Ask        func(ctx context.Context, in io.Reader, out io.Writer, prompt, initial string) (string, error)
	EditPreset func(ctx context.Context, in io.Reader, out io.Writer, form app.PresetForm, load func(context.Context) []app.RecentVar) (app.PresetForm, error)
	GOOS       string
	// Glob is filepath.Glob; nil matches nothing. It finds the Windows
	// presets file from WSL.
	Glob func(pattern string) ([]string, error)
	// WindowsHome returns the Windows home as a WSL path. It is asked only in
	// WSL, when bam creates the presets file.
	WindowsHome func() (string, error)
}

// SystemEnv returns the Env of the running process.
func SystemEnv() Env {
	wd, _ := os.Getwd()
	home, err := os.UserHomeDir()
	if err != nil {
		// Without a home directory, root bam's files at / so they never
		// resolve against the working directory.
		home = string(filepath.Separator)
	}
	dirs := config.LocateDirs(home, goruntime.GOOS, os.Getenv)
	machine := os.Getenv("BAM_CONFIG")
	if machine == "" {
		machine = filepath.Join(dirs.Config, "config.yaml")
	}
	out := int(os.Stdout.Fd())
	return Env{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Getenv:    os.Getenv,
		StdoutTTY: term.IsTerminal(out),
		StderrTTY: term.IsTerminal(int(os.Stderr.Fd())),
		StdinTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		Width: func() int {
			if w, _, err := term.GetSize(out); err == nil && w > 0 {
				return w
			}
			if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 0 {
				return c
			}
			return 80
		},
		WorkDir:     wd,
		GitBranch:   gitBranch,
		GitHead:     gitHead,
		Home:        home,
		Glob:        filepath.Glob,
		WindowsHome: wslWindowsHome,
		Paths: Paths{
			MachineConfig:   machine,
			Credentials:     filepath.Join(dirs.Config, "credentials.yaml"),
			Capabilities:    filepath.Join(dirs.Cache, "capabilities.json"),
			State:           filepath.Join(dirs.State, "state.json"),
			Usage:           filepath.Join(dirs.State, "usage.json"),
			Sets:            filepath.Join(dirs.State, "sets"),
			Trees:           filepath.Join(dirs.Cache, "tree"),
			AgentJobs:       filepath.Join(dirs.Cache, "agent-jobs"),
			JobAgents:       filepath.Join(dirs.Cache, "job-agents"),
			LegacyConfigDir: dirs.OldConfig,
		},
		Clock:   app.SystemClock{},
		Keyring: credential.SystemKeyring{},
		Connect: func(o bamboo.Options) (Backend, error) {
			c, err := bamboo.New(o)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		OpenBrowser: func(u string) error {
			return openURL(u, browser.OpenURL, config.IsWSL(os.Getenv), runWindowsOpener)
		},
		Notify:    desktopNotify,
		RunPager:  runPager,
		RunEditor: runEditor,
		ReadSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(b), err
		},
		RunTUI:     tui.Run,
		PickPreset: tui.PickPreset,
		Choose:     tui.Choose,
		Ask:        tui.Ask,
		EditPreset: tui.EditPresetForm,
		GOOS:       goruntime.GOOS,
	}
}

// wslWindowsHome asks Windows for %USERPROFILE% and converts it to a WSL
// path, such as /mnt/c/Users/jdoe.
func wslWindowsHome() (string, error) {
	cmd := exec.Command("cmd.exe", "/c", "echo %USERPROFILE%")
	cmd.Dir = "/mnt/c" // cmd.exe warns about a Linux working directory
	out, err := cmd.Output()
	win := strings.TrimSpace(string(out))
	if err != nil || win == "" || strings.Contains(win, "%") {
		return "", errors.New("cannot read %USERPROFILE% from cmd.exe")
	}
	out, err = exec.Command("wslpath", "-u", win).Output()
	if err != nil {
		return "", errors.New("wslpath cannot convert " + win)
	}
	return strings.TrimSpace(string(out)), nil
}
