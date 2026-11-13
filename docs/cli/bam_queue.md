## bam queue

List the builds waiting for an agent, first in line first

### Synopsis

List the builds waiting for an agent, first in line first. A queued build
that does not start is waiting for an agent: bam agent list shows which are
online, enabled and busy.

The WHY column says why a build waits: no agent can run it, agents can run it
but all are busy, offline or disabled, or agents are free and it is next. It
names the agents when three or fewer can run the job. Bamboo shows whether an
agent can run a job to every user, but which agents can, and whether they are
free, only to an administrator. A part Bamboo refuses is left out, never an
error.

```
bam queue [flags]
```

### Examples

```
  bam queue
  bam queue --json
```

### Options

```
  -h, --help   help for queue
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

