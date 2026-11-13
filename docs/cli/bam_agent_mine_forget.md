## bam agent mine forget

Forget agents bam remembers, so they stop showing as down

### Synopsis

bam remembers the names of your agents it has seen, so an agent Bamboo no longer
lists shows as down. Forget a name to stop that at once, for an agent you retired;
bam forgets a name by itself after 30 days.

```
bam agent mine forget NAME... [flags]
```

### Examples

```
  bam agent mine forget team-a-agent-pool-7
```

### Options

```
  -h, --help   help for forget
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

* [bam agent mine](bam_agent_mine.md)	 - The agents you support: add, rm, list, forget

