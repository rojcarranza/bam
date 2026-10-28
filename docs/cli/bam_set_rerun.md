## bam set rerun

Retry the failed runs of a set run, then finish the train

### Synopsis

Retry a failed set run under its own id. In the stage that failed, Bamboo
restarts each failed build: its failed jobs run again, with the same variables
and commit, under the same build key. A run that never triggered is triggered.
When that stage succeeds, the later stages run. Runs that succeeded are left
alone, and no build is ever stopped.

```
bam set rerun [set|id] --failed [flags]
```

### Examples

```
  bam set rerun --failed
  bam set rerun release --failed
  bam set rerun release-20260929T101500Z --failed --var version=1.4.0
```

### Options

```
      --failed            retry the runs that failed (required)
      --file string       the train file of a file set, when it has moved since the run
      --force             take over the lock of a set run whose process is gone
  -h, --help              help for rerun
      --var stringArray   variable name=value; give again every --var the run was started with (repeatable)
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

* [bam set](bam_set.md)	 - Release trains: presets run in ordered stages

