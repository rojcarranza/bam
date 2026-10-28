package bamboo

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Capabilities records which optional Bamboo REST features a server supports.
// Empty strings mean "not yet known".
type Capabilities struct {
	Version   string    `json:"version,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	PlanVars  string    `json:"plan_vars,omitempty"`  // variables | variable | none
	BuildVars string    `json:"build_vars,omitempty"` // yes | no
	// FailedTestsFrom is where failed tests are read: the build's result, or
	// each failed job's (Bamboo 12 reports them per job only). It replaced
	// failed_tests, which learned "no" from build results on such servers.
	FailedTestsFrom string `json:"failed_tests_from,omitempty"` // build | jobs | no
	Log             string `json:"log,omitempty"`               // entries | download
	Stop            string `json:"stop,omitempty"`              // yes | no
	Rerun           string `json:"rerun,omitempty"`             // yes | no
	Progress        string `json:"progress,omitempty"`          // yes | no
}

const capsMaxAge = 7 * 24 * time.Hour

// LoadCapabilities reads the cached capabilities of origin from path.
func LoadCapabilities(path, origin string) (Capabilities, error) {
	all, err := readCapsFile(path)
	if err != nil {
		return Capabilities{}, err
	}
	return all[origin], nil
}

// CannotRestart reports whether c, learned within the last week, records
// that the server refuses restarts. It reads only the cache, never the server.
func (c Capabilities) CannotRestart(now time.Time) bool {
	return c.Rerun == "no" && now.Sub(c.CheckedAt) <= capsMaxAge
}

// CannotStop is CannotRestart for stopping a build.
func (c Capabilities) CannotStop(now time.Time) bool {
	return c.Stop == "no" && now.Sub(c.CheckedAt) <= capsMaxAge
}

// SaveCapabilities writes the capabilities of origin into path, keeping other origins.
func SaveCapabilities(path, origin string, c Capabilities) error {
	all, err := readCapsFile(path)
	if err != nil {
		all = map[string]Capabilities{} // a corrupt cache is rebuilt, not fatal
	}
	all[origin] = c
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readCapsFile(path string) (map[string]Capabilities, error) {
	all := map[string]Capabilities{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	return all, nil
}

// Capabilities returns what the client currently knows.
func (c *Client) Capabilities() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// fresh returns the capabilities, discarding a cached set once per process
// when it is older than seven days or the server version changed.
func (c *Client) fresh(ctx context.Context) Capabilities {
	c.mu.Lock()
	check := !c.versionChecked && c.caps != (Capabilities{})
	c.versionChecked = true
	caps := c.caps
	c.mu.Unlock()
	if !check {
		return caps
	}
	stale := c.now().Sub(caps.CheckedAt) > capsMaxAge
	if !stale {
		if info, err := c.ServerInfo(ctx); err == nil && info.Version != caps.Version {
			stale = true
		}
	}
	if stale {
		c.mu.Lock()
		c.caps = Capabilities{}
		caps = c.caps
		c.mu.Unlock()
	}
	return caps
}

// learn records a capability, stamps the version and time, and saves.
func (c *Client) learn(ctx context.Context, set func(*Capabilities)) {
	c.mu.Lock()
	needVersion := c.caps.Version == ""
	c.mu.Unlock()
	version := ""
	if needVersion {
		if info, err := c.ServerInfo(ctx); err == nil {
			version = info.Version
		}
	}
	c.mu.Lock()
	set(&c.caps)
	if version != "" {
		c.caps.Version = version
	}
	c.caps.CheckedAt = c.now()
	snapshot := c.caps
	c.mu.Unlock()
	c.saveCaps(snapshot)
}

// isUnsupportedStatus reports statuses that mean "this endpoint does not exist here".
func isUnsupportedStatus(err error) bool {
	switch statusOf(err) {
	case 400, 404, 405, 501:
		return true
	}
	return false
}
