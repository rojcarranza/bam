## bam build rerun

Rerun a failed build's failed jobs

### Synopsis

Rerun a failed or stopped build's failed jobs, as Bamboo's "Rerun failed jobs"
does: same build key, same variables and commit. A build that is running or
succeeded is refused.

```
bam build rerun [<build>|<plan>|<preset>] [flags]
```

### Options

```
      --branch string      plan branch when a plan or preset is given
      --dry-run            show which build would rerun, restart nothing
  -h, --help               help for rerun
      --last               the last build bam triggered from this repository
      --timeout duration   with --watch, stop watching after this long (the build keeps running)
      --watch              follow the build and the builds it triggers until they finish
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

* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel

