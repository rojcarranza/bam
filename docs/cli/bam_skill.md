## bam skill

Print the skill that teaches AI agents to use bam, or install it

### Synopsis

bam skill prints the SKILL.md that teaches an AI agent (Claude Code and others that
read the same format) how to use bam: read first, ask before writing, read the exit
code, and start with --read-only.

bam skill            prints it
bam skill install    writes it to ~/.claude/skills/bam/SKILL.md, or into --dir DIR/bam/SKILL.md

```
bam skill [install] [flags]
```

### Examples

```
  bam skill > SKILL.md
  bam skill install
  bam skill install --dir ~/.agents/skills
```

### Options

```
      --dir string          skills folder to install into (default ~/.claude/skills)
  -h, --help[=true|false]   help for skill
```

### Options inherited from parent commands

```
      --color string             color output: auto, always or never
      --debug[=true|false]       log HTTP requests to stderr
      --json[=true|false]        print JSON
      --no-tui[=true|false]      never open the terminal UI
      --read-only[=true|false]   refuse every command that changes something in Bamboo (also BAM_READ_ONLY=1 or read_only: true in the config)
      --server string            server alias to use
```

### SEE ALSO

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo

