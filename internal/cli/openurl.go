package cli

import (
	"errors"
	"os/exec"
)

// openURL opens url with the Linux opener first. In WSL, where a distro often
// has no xdg-open, it falls back to the Windows default browser. linux and
// windows are fields so tests stand in for both.
func openURL(url string, linux func(string) error, wsl bool, windows func(url string) error) error {
	err := linux(url)
	if err == nil || !wsl {
		return err
	}
	if werr := windows(url); werr != nil {
		return errors.Join(err, werr)
	}
	return nil
}

// runWindowsOpener hands url to the Windows shell from WSL: wslview when the
// wslu package is installed, else rundll32.exe, which takes the URL as one
// argument with no shell quoting to get wrong.
func runWindowsOpener(url string) error {
	if p, err := exec.LookPath("wslview"); err == nil {
		return exec.Command(p, url).Run()
	}
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Run()
}
