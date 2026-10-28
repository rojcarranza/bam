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

	"github.com/adrg/xdg"
	"github.com/pkg/browser"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/r0jjames/bam-cli/internal/view/tui"
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
	Sets          string // directory of set run records and locks
	Trees         string // directory of saved build trees, one file per server
}

// Env is everything the CLI takes from the process. Tests build it by hand.
type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	StdoutTTY   bool
	StderrTTY   bool
	StdinTTY    bool
	Width       func() int
	WorkDir     string
	Home        string
	Paths       Paths
	Clock       app.Clock
	Keyring     credential.Keyring
	Connect     func(bamboo.Options) (Backend, error)
	OpenBrowser func(url string) error
	RunPager    func(cmd string, r io.Reader) error
	RunEditor   func(cmd string, initial []byte) ([]byte, error)
	ReadSecret  func() (string, error)
	// RunTUI opens the terminal UI. It is a field so tests can assert on the
	// Deps the CLI builds without starting a terminal program.
	RunTUI func(context.Context, tui.Deps) error
	GOOS   string
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
	home, _ := os.UserHomeDir()
	machine := os.Getenv("BAM_CONFIG")
	if machine == "" {
		machine = filepath.Join(xdg.ConfigHome, "bam", "config.yaml")
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
		Home:        home,
		Glob:        filepath.Glob,
		WindowsHome: wslWindowsHome,
		Paths: Paths{
			MachineConfig: machine,
			Credentials:   filepath.Join(xdg.ConfigHome, "bam", "credentials.yaml"),
			Capabilities:  filepath.Join(xdg.CacheHome, "bam", "capabilities.json"),
			State:         filepath.Join(xdg.StateHome, "bam", "state.json"),
			Sets:          filepath.Join(xdg.StateHome, "bam", "sets"),
			Trees:         filepath.Join(xdg.CacheHome, "bam", "tree"),
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
		OpenBrowser: browser.OpenURL,
		RunPager:    runPager,
		RunEditor:   runEditor,
		ReadSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(b), err
		},
		RunTUI: tui.Run,
		GOOS:   goruntime.GOOS,
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
