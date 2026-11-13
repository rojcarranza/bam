## bam set history

List past set runs of this repository, newest first

### Synopsis

List past set runs of this repository, newest first, with the states bam recorded.
bam keeps the last 20 runs of each set. Use bam set status <id> for a run's live build states.

```
bam set history [set] [flags]
```

### Examples

```
  bam set history
  bam set history release -n 5
```

### Options

```
  -h, --help[=true|false]   help for history
  -n, --limit int           number of runs (default 20)
```

### Options inherited from parent commands

```
      --color string          color output: auto, always or never
      --debug[=true|false]    log HTTP requests to stderr
      --json[=true|false]     print JSON
      --no-tui[=true|false]   never open the terminal UI
      --server string         server alias to use
```

### SEE ALSO

* [bam set](bam_set.md)	 - Release trains: presets run in ordered stages

