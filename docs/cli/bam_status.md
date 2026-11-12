## bam status

Show every build you started with bam in the last day, and how it is going

### Synopsis

Show every build you started with bam in the last day, across repositories and
servers, newest first, with its live state, progress and link. Inside a
repository the build of the checked-out branch comes first, marked →.

bam keeps the last 50 runs it started: build, plan, preset, server and time,
never a variable value.

--short prints one line for a shell prompt: the first build, its state and
progress, and how many others are running. It prints nothing when there is
nothing to show.

```
bam status [flags]
```

### Examples

```
  bam status
  bam status --short
  bam status --json
```

### Options

```
  -h, --help    help for status
      --short   print one line for a shell prompt
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

