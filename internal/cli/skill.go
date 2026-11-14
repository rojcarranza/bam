package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rojcarranza/bam/internal/atomicfile"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/skills"
	"github.com/spf13/cobra"
)

func init() {
	commandSets = append(commandSets, func(root *cobra.Command, r *runtime) { root.AddCommand(newSkillCmd(r)) })
}

func newSkillCmd(r *runtime) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:     "skill [install]",
		Short:   "Print the skill that teaches AI agents to use bam, or install it",
		GroupID: groupSetup,
		Long: "bam skill prints the SKILL.md that teaches an AI agent (Claude Code and others that\n" +
			"read the same format) how to use bam: read first, ask before writing, read the exit\n" +
			"code, and start with --read-only.\n\n" +
			"bam skill            prints it\n" +
			"bam skill install    writes it to ~/.claude/skills/bam/SKILL.md, or into --dir DIR/bam/SKILL.md",
		Example: "  bam skill > SKILL.md\n  bam skill install\n  bam skill install --dir ~/.agents/skills",
		Args:    cobra.MaximumNArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if dir != "" {
					return errs.Usagef("--dir is for bam skill install").WithTry("bam skill install --dir " + dir)
				}
				_, err := fmt.Fprint(r.env.Stdout, skills.Bam)
				return err
			}
			if args[0] != "install" {
				return errs.Usagef("unknown argument %q", args[0]).WithTry("bam skill install")
			}
			if dir == "" {
				dir = filepath.Join(r.env.Home, ".claude", "skills")
			}
			path := filepath.Join(dir, "bam", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return errs.Configf("cannot create %s", filepath.Dir(path)).WithWhy(err.Error())
			}
			if err := atomicfile.Write(path, []byte(skills.Bam), 0o644); err != nil {
				return errs.Configf("cannot write %s", path).WithWhy(err.Error())
			}
			fmt.Fprintf(r.env.Stdout, "installed the bam skill at %s\n", path)
			return nil
		}),
	}
	cmd.Flags().StringVar(&dir, "dir", "", "skills folder to install into (default ~/.claude/skills)")
	return cmd
}
