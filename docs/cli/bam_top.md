## bam top

Print what needs you: agents, the queue and your pinned pipelines

### Synopsis

Print one board of the server: its version, what needs you, the agent counts and
every agent that is offline or disabled, the queue with the time each build
waited, and your pinned pipelines by group with their last 10 results.

Needs you lists an enabled agent that is offline, a build that waited more than
5 minutes, a pinned pipeline whose last build failed, and a pinned build running
half again past its usual time.

With agents: set (bam agent mine add NAME-START) the board shows only your
agents, each building, idle, disabled or down, and Needs you raises your agents
that are down or disabled. --all shows every agent of the server.

Pin pipelines with bam pin add, or list groups under monitor: in the machine file
or .bam.yaml. When Bamboo refuses the agent list or the queue (the agent list
needs an administrator), the board says so in one line and prints the rest.
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
      --all    show every agent of the server, not only the ones you listed with bam agent mine
  -h, --help   help for top
      --once   print the board once instead of opening the live board
```

### Options inherited from parent commands

```
      --color string    color output: auto, always or never
      --debug           log HTTP requests to stderr
      --json            print JSON
      --no-tui          never open the terminal UI
      --server string   server alias to use
```

### SEE ALSO

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo

