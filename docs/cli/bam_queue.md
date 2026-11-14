## bam queue

List the builds waiting for an agent, first in line first

### Synopsis

List the builds waiting for an agent, first in line first. A queued build
that does not start is waiting for an agent: bam agent list shows which are
online, enabled and busy.

The WHY column says why a build waits: no agent can run it, agents can run it
but all are busy, offline or disabled, agents are free and it is next, or
agents are free and other builds come before it. It names the agents when
three or fewer can run the job. Bamboo shows which agents can run a job, and
whether they are free, only to an administrator. A part Bamboo refuses is left
out, never an error.

bam reads which jobs each agent can run once an hour and keeps them in its
cache, so a change to an agent's capabilities shows within the hour, and a
build queued since then that no agent lists may have no WHY until then.

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
  -h, --help[=true|false]   help for queue
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

