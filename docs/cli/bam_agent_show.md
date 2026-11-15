## bam agent show

Show an agent's track record: what it builds, its latest jobs, how often they fail

### Synopsis

Show one agent's state, the jobs it runs now, its latest finished jobs and the share
of its jobs that failed beside the other agents'. Bamboo's job records do not say
which agent ran a job, so bam reads the first lines of each job's log in the newest
builds of every plan and remembers the answer for finished jobs. It needs Bamboo's
agent list, which only administrators can read.

```
bam agent show NAME [flags]
```

### Examples

```
  bam agent show linux-3
  bam agent show linux-3 --builds 100
  bam agent show linux-3 --json
```

### Options

```
      --builds int          how many of the newest builds to look through (default 40)
  -h, --help[=true|false]   help for show
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

* [bam agent](bam_agent.md)	 - Build agents: list, show, mine, disable, enable

