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
  -h, --help[=true|false]    help for status
      --short[=true|false]   print one line for a shell prompt
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

