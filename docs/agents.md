# Using bam with AI agents

bam ships a skill that teaches an AI agent (Claude Code and others that read `SKILL.md` files)
how to use it, and a read-only switch that makes sure the agent cannot change anything in Bamboo.

## The skill

    bam skill install                    # writes ~/.claude/skills/bam/SKILL.md
    bam skill install --dir ~/.agents/skills
    bam skill > SKILL.md                 # print it instead

The skill tells the agent to read first, pass `--json --no-tui`, act on the exit code, confirm with you
before `run`, `cancel`, `build rerun`, `build tree --rerun-failed`, `set run`, `set resume`, `set rerun`, `agent disable` or `agent enable`, and never ask for a token. It is embedded in the
binary, so a `go install` build carries it, and it is also in the repository at `skills/bam/SKILL.md`.
A test checks that every command and flag the skill shows exists.

## The read-only switch

Any one of these turns it on:

| How | Scope |
| --- | --- |
| `bam --read-only ...` | that command |
| `BAM_READ_ONLY=1` in the environment | every bam the shell starts, so set it where the agent runs |
| `read_only: true` in `~/.config/bam/config.yaml` | every bam on this computer |

The environment variable and the flag cannot turn off `read_only: true` in the machine file, and a
repository's `.bam.yaml` cannot set or clear it, so a teammate's repository cannot lift your switch.
Handing an agent `BAM_READ_ONLY=1` is the usual way to let it look but not touch.

With it on, bam refuses what changes Bamboo. The commands refuse before they ask a question, take a lock or write a record; the terminal UI may ask its usual confirmation first and then refuses, and a set run is refused before it takes a lock:

- `bam run`, `bam build run`
- `bam cancel`, `bam build cancel`
- `bam build rerun`, `bam build tree --rerun-failed`
- `bam set run`, `bam set resume`, `bam set rerun`
- `bam agent disable`, `bam agent enable`
- the same actions in the terminal UI

A refused command prints why on stderr and exits 2:

    error: read-only mode: refused to run a plan
      bam is set read-only, so it will not change anything in Bamboo
      try: run without --read-only, and unset BAM_READ_ONLY and read_only in the bam config, to allow it

Everything that only reads still works, and so does `--dry-run`, which resolves and validates and
sends nothing. As a second line of defence the connection to Bamboo itself refuses `Trigger`,
`StopBuild`, `RestartBuild` and the agent switch in this mode, and tests fail when a write method is added to the
provider without being classified or without a refusal, so a later command cannot slip past the switch. `bam pin` and `bam agent mine` change local files only and stay allowed.
