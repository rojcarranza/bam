## bam queue

List the builds waiting for an agent, first in line first

### Synopsis

List the builds waiting for an agent, first in line first. A queued build
that does not start is waiting for an agent: bam agent list shows which are
online, enabled and busy.

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

