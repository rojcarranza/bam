package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The skill is the captain's ask: bam ships a skill that teaches AI agents to
// use it. It must stay true: every command and flag it shows must exist.

func TestSkillPrintsAndInstalls(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("skill"), h.stderr.String())
	assert.Equal(t, skills.Bam, h.stdout.String())

	require.Equal(t, 0, h.run("skill", "install"), h.stderr.String())
	got, err := os.ReadFile(filepath.Join(h.home, ".claude", "skills", "bam", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, skills.Bam, string(got))

	dir := t.TempDir()
	require.Equal(t, 0, h.run("skill", "install", "--dir", dir), h.stderr.String())
	assert.FileExists(t, filepath.Join(dir, "bam", "SKILL.md"))

	assert.Equal(t, 2, h.run("skill", "nope"))
	assert.Equal(t, 2, h.run("skill", "--dir", dir), "--dir needs install")
}

func TestSkillHasTheFrontmatterAgentsRead(t *testing.T) {
	front, _, ok := strings.Cut(strings.TrimPrefix(skills.Bam, "---\n"), "\n---\n")
	require.True(t, ok, "SKILL.md opens with a frontmatter block")
	assert.Contains(t, front, "name: bam\n")
	assert.Regexp(t, `(?m)^description: .{40,}`, front)
}

func TestSkillShowsOnlyCommandsAndFlagsThatExist(t *testing.T) {
	h := newHarness(t)
	root := newRoot(&runtime{env: h.env})
	block := regexp.MustCompile("(?s)```bash\n(.*?)```")
	checked := 0
	for _, m := range block.FindAllStringSubmatch(skills.Bam, -1) {
		for line := range strings.SplitSeq(m[1], "\n") {
			line, _, _ = strings.Cut(line, " #")
			words := strings.Fields(line)
			if len(words) < 2 || words[0] != "bam" {
				continue
			}
			cmd, rest, err := root.Find(words[1:])
			require.NoError(t, err, line)
			require.NotEqual(t, root, cmd, "%q names no command", line)
			for _, w := range rest {
				if name, ok := strings.CutPrefix(w, "--"); ok {
					name, _, _ = strings.Cut(name, "=")
					assert.NotNil(t, cmd.Flags().Lookup(name), "%q: %s has no --%s", line, cmd.CommandPath(), name)
				}
			}
			checked++
		}
	}
	assert.Greater(t, checked, 15, "the skill shows its commands in bash blocks")
}

func TestSkillNamesTheExitCodesBamUses(t *testing.T) {
	for _, row := range []string{"| 0 |", "| 1 |", "| 2 |", "| 3 |", "| 4 |", "| 5 |", "| 6 |", "| 130 |"} {
		assert.Contains(t, skills.Bam, row)
	}
	assert.Contains(t, skills.Bam, "read-only mode")
}
