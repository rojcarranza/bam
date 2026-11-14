package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/readonly"
	"github.com/rojcarranza/bam/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
	var meta struct{ Name, Description string }
	require.NoError(t, yaml.Unmarshal([]byte(front), &meta), "the frontmatter is valid YAML")
	assert.Equal(t, "bam", meta.Name)
	assert.NotEmpty(t, meta.Description)
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

func TestSkillExitCodesMatchWhatBamReturns(t *testing.T) {
	// The table in the skill is read back and compared with exitCode itself.
	table := map[int]string{}
	for _, m := range regexp.MustCompile(`(?m)^   \| (\d+) \| (.+) \|$`).FindAllStringSubmatch(skills.Bam, -1) {
		code, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		table[code] = m[2]
	}
	want := map[int]error{
		1:   &resultError{State: provider.StateFailed},
		2:   errs.Usagef("x"),
		3:   errs.Configf("x"),
		4:   errs.New(errs.KindAuth, "x"),
		5:   errs.Bamboof("x"),
		6:   errs.New(errs.KindTimeout, "x"),
		130: context.Canceled,
	}
	for code, err := range want {
		assert.Equal(t, code, exitCode(err))
		assert.NotEmpty(t, table[code], "the skill explains exit %d", code)
	}
	assert.NotEmpty(t, table[0])
	assert.Len(t, table, len(want)+1, "the skill names no code bam never returns")
	assert.Equal(t, 2, exitCode(readonly.Refusal("x")), "a refusal is exit 2")
}
