package app

import (
	"regexp"
	"strings"
)

// ErrorBlock is one error found in a job log, with the lines around it.
type ErrorBlock struct {
	Line    int      // 1-based line number of the error
	Text    string   // the error line, without terminal colors
	Pattern string   // name of the logPattern that matched
	Start   int      // 1-based line number of Context[0]
	Context []string // the error line and up to n lines either side
	Count   int      // times the same text appears in the log
}

// logPatterns are the error lines bam knows, by tool. The first match names
// the line; Bamboo's own "Failing task" line comes last because tools print
// the real cause before it.
var logPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"error", regexp.MustCompile(`^[\s│|]*Error: `)}, // Terraform, Helm, kubectl
	{"fatal", regexp.MustCompile(`^\s*fatal: `)},     // Ansible, git
	{"maven", regexp.MustCompile(`^\s*\[ERROR\] `)},
	{"gradle", regexp.MustCompile(`^FAILURE: `)},
	{"go-test", regexp.MustCompile(`^\s*--- FAIL: `)},
	{"npm", regexp.MustCompile(`^npm (ERR!|error) `)},
	{"panic", regexp.MustCompile(`^panic: `)},
	{"traceback", regexp.MustCompile(`^Traceback \(most recent call last\):`)},
	{"exception", regexp.MustCompile(`\b[\w.$]*(Exception|Error)(: |$)`)},
	{"bamboo-task", regexp.MustCompile(`^Failing task since return code`)},
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// ErrorBlocks finds the error lines in a job log, in log order, so the first
// block is the likely cause. A text seen again only raises the first block's
// Count. It returns at most limit blocks (0 = all), each with n lines of
// context either side.
func ErrorBlocks(lines []string, n, limit int) []ErrorBlock {
	n = max(n, 0) // a negative context would slice backwards and panic
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = ansiRe.ReplaceAllString(l, "")
	}
	var out []ErrorBlock
	seen := map[string]int{}
	for i, l := range clean {
		name := matchPattern(l)
		if name == "" {
			continue
		}
		text := strings.TrimSpace(l)
		if j, ok := seen[text]; ok {
			out[j].Count++
			continue
		}
		if limit > 0 && len(out) == limit {
			continue
		}
		from, to := max(0, i-n), min(len(clean), i+n+1)
		seen[text] = len(out)
		out = append(out, ErrorBlock{
			Line: i + 1, Text: text, Pattern: name,
			Start: from + 1, Context: clean[from:to], Count: 1,
		})
	}
	return out
}

func matchPattern(line string) string {
	for _, p := range logPatterns {
		if p.re.MatchString(line) {
			return p.name
		}
	}
	return ""
}
