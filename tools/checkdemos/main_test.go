package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The captain's ask for the README GIFs: each one is real usage of bam, from a tape that can be
// re-recorded, and shows placeholders only. check reports a GIF without a tape, a tape without a
// GIF, a README link to a missing GIF, and screen text with the lab's address or a real name.
func TestCheckDemos(t *testing.T) {
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "![a](docs/demos/gifs/run-watch.gif)\n![b](docs/demos/gifs/logs.gif)\n![c](docs/demos/gifs/lost.gif)\n")
	write("docs/demos/gifs/run-watch.gif", "GIF89a")
	write("docs/demos/tapes/run-watch.tape", "Output gifs/run-watch.gif")
	write("docs/demos/text/run-watch.txt", "$ bam run ci\nSETLIST-CI-5 success https://bamboo.example.com/browse/SETLIST-CI-5\n")
	write("docs/demos/gifs/logs.gif", "GIF89a")
	write("docs/demos/tapes/logs.tape", "x")
	write("docs/demos/text/logs.txt", "see http://localhost:8085/browse/SETLIST-CI-5\n")
	write("docs/demos/tapes/orphan.tape", "x")
	write("docs/demos/tapes/_common.tape", "x")

	got := strings.Join(check(root), "\n")
	for _, want := range []string{
		`README shows docs/demos/gifs/lost.gif, which does not exist`,
		`docs/demos/tapes/orphan.tape has no GIF`,
		`docs/demos/text/logs.txt shows "localhost"`,
		`docs/demos/text/logs.txt shows ":8085"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing problem %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "run-watch") {
		t.Errorf("a complete demo was reported:\n%s", got)
	}
}
