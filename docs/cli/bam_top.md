## bam top

Print what needs you: agents, what they build and your pinned pipelines

### Synopsis

Print one board of the server: its version, what needs you, the agent counts and
every agent that is offline or disabled, and your pinned pipelines by group
with their last 10 results.

Needs you lists an enabled agent that is offline, a build of a pinned pipeline
that waited more than 5 minutes for an agent, a pinned pipeline whose last
build failed, and a pinned build running half again past its usual time.

With agents: set (bam agent mine add NAME-START) the board shows only your
agents, each building, idle, disabled or down, and Needs you raises your agents
that are down or disabled. On the live board each building agent has a row with
the job it runs, its plan and branch, its pin group and how long it runs, and
the idle agents of a pool are named on one line. --all shows every agent of the
server.

With --tree, each pin that started other pipelines prints them as bam build
tree does, under its group's table; with --json each pin gains tree. The live board
always draws them (space folds a tree).

Pin pipelines with bam pin add, or list groups under monitor: in the machine file
or .bam.yaml. When Bamboo refuses the agent list (it needs an administrator), the
board says so in one line and prints the rest.
bam top only reads from Bamboo.

On a terminal bam top opens the live board, which refreshes by itself (see
docs/tui.md). With --once, --json or a pipe it prints the board once.

```
bam top [flags]
```

### Examples

```
  bam top
  bam top --once
  bam top --json
  bam pin add integration PROJ-BUILD
```

### Options

```
      --all[=true|false]    show every agent of the server, not only your part
  -h, --help[=true|false]   help for top
      --once[=true|false]   print the board once instead of opening the live board
      --tree[=true|false]   print the tree of child pipelines under each pin (with --once or --json)
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

