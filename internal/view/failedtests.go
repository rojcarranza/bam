package view

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/rojcarranza/bam/internal/provider"
)

const (
	maxShownTests    = 5   // tests listed in text output
	maxMessageLines  = 6   // lines of one test's message in text output
	maxMessageLength = 400 // characters of one test's message in text output
	maxJSONMessage   = 4000
)

// secretPairRe is a secret-looking name followed by its value: password=x,
// "token": "x", Authorization: Bearer x, accessToken=x. The name and
// separator stay.
var secretPairRe = regexp.MustCompile(`((?:(?:^|[^A-Za-z])(?i:pass(?:word|phrase)|secret|sshkey|token|api[_-]?key|authorization)|[a-z](?:Pass(?:word|phrase)|Secret|Ssh[Kk]ey|Token|Api[_-]?Key|Authorization))(?:[_-]\w*|[A-Z]\w*)?["']?\s*[=:]\s*)(?:("(?:(?i:bearer|basic)\s+)?)[^"\n]+|('(?:(?i:bearer|basic)\s+)?)[^'\n]+|((?:(?i:bearer|basic)\s+)?)[^\s"',;]+)`)

// MaskMessage hides the values of secret-looking name/value pairs in a test
// message, the same names bam masks in variables, and bounds its length.
func MaskMessage(m string) string {
	m = secretPairRe.ReplaceAllString(m, "${1}${2}${3}${4}********")
	if len(m) > maxJSONMessage {
		m = truncate(m, maxJSONMessage) + "..."
	}
	return m
}

// truncate cuts s to at most n bytes without splitting a UTF-8 character.
func truncate(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// textMessage is MaskMessage cut to what text output shows.
func textMessage(m string) []string {
	lines := strings.Split(strings.TrimSpace(MaskMessage(m)), "\n")
	if len(lines) > maxMessageLines {
		lines = append(lines[:maxMessageLines], "...")
	}
	out := lines[:0]
	for _, l := range lines {
		if len(l) > maxMessageLength {
			l = truncate(l, maxMessageLength) + "..."
		}
		out = append(out, strings.TrimRight(l, " \t\r"))
	}
	return out
}

// writeFailedTests prints a build's failed tests under label. Without
// messages it is one comma-separated line; with any, each test gets its
// name and its message beneath.
func writeFailedTests(w io.Writer, label string, b provider.Build) {
	withMessage := false
	for _, t := range b.FailedTestDetails {
		withMessage = withMessage || strings.TrimSpace(t.Message) != ""
	}
	if !withMessage {
		shown, more := b.FailedTests, ""
		if len(shown) > maxShownTests {
			more = fmt.Sprintf(" (+%d more)", len(shown)-maxShownTests)
			shown = shown[:maxShownTests]
		}
		fmt.Fprintf(w, "%s: %s%s\n", label, strings.Join(shown, ", "), more)
		return
	}
	fmt.Fprintf(w, "%s:\n", label)
	for i, t := range b.FailedTestDetails {
		if i == maxShownTests {
			fmt.Fprintf(w, "  (+%d more)\n", len(b.FailedTestDetails)-maxShownTests)
			break
		}
		fmt.Fprintf(w, "  %s\n", t.Name)
		if strings.TrimSpace(t.Message) != "" {
			for _, l := range textMessage(t.Message) {
				fmt.Fprintf(w, "      %s\n", l)
			}
		}
	}
}
