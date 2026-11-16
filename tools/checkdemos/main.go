// Command checkdemos checks the README demo GIFs: every GIF the README shows has a tape
// and screen text, every tape has its GIF, each GIF stays small, and the screen text names
// neither the lab's own address nor anything personal.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// maxGIFBytes keeps the README light to load.
const maxGIFBytes = 2 << 20

// denied is what no recording may show: the lab's own address and mail addresses. The
// recordings show placeholders only. BAM_DEMO_DENY adds more, comma separated: the name of
// the recording computer and of its user, which this public file must not spell out.
var denied = deniedTerms(os.Getenv("BAM_DEMO_DENY"))

func deniedTerms(extra string) []string {
	out := []string{"localhost", "127.0.0.1", ":8085", "gmail", "@users.noreply"}
	for _, t := range strings.Split(extra, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

var gifRef = regexp.MustCompile(`docs/demos/gifs/([a-z0-9-]+)\.gif`)

func main() {
	problems := check(".")
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
}

// check returns every problem found under the repository root, sorted.
func check(root string) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	demos := filepath.Join(root, "docs", "demos")

	shown := map[string]bool{}
	if readme, err := os.ReadFile(filepath.Join(root, "README.md")); err == nil {
		for _, m := range gifRef.FindAllStringSubmatch(string(readme), -1) {
			shown[m[1]] = true
		}
	}
	names := func(dir, ext string) map[string]bool {
		set := map[string]bool{}
		entries, _ := os.ReadDir(filepath.Join(demos, dir))
		for _, e := range entries {
			n := e.Name()
			if strings.HasSuffix(n, ext) && !strings.HasPrefix(n, "_") {
				set[strings.TrimSuffix(n, ext)] = true
			}
		}
		return set
	}
	tapes, gifs, texts := names("tapes", ".tape"), names("gifs", ".gif"), names("text", ".txt")

	for n := range shown {
		if !gifs[n] {
			add("README shows docs/demos/gifs/%s.gif, which does not exist (make demo NAME=%s)", n, n)
		}
	}
	for n := range gifs {
		if !shown[n] {
			add("docs/demos/gifs/%s.gif is not in the README", n)
		}
		if !tapes[n] {
			add("docs/demos/gifs/%s.gif has no tape docs/demos/tapes/%s.tape", n, n)
		}
		if !texts[n] {
			add("docs/demos/gifs/%s.gif has no screen text docs/demos/text/%s.txt", n, n)
		}
		if fi, err := os.Stat(filepath.Join(demos, "gifs", n+".gif")); err == nil && fi.Size() > maxGIFBytes {
			add("docs/demos/gifs/%s.gif is %d KB; the limit is %d KB", n, fi.Size()>>10, maxGIFBytes>>10)
		}
	}
	for n := range tapes {
		if !gifs[n] {
			add("docs/demos/tapes/%s.tape has no GIF (make demo NAME=%s)", n, n)
		}
	}
	for n := range texts {
		b, err := os.ReadFile(filepath.Join(demos, "text", n+".txt"))
		if err != nil {
			continue
		}
		low := strings.ToLower(string(b))
		for _, d := range denied {
			if strings.Contains(low, strings.ToLower(d)) {
				add("docs/demos/text/%s.txt shows %q", n, d)
			}
		}
	}
	sort.Strings(out)
	return out
}
