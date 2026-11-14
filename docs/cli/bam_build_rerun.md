## bam build rerun

Rerun a failed build's failed jobs

### Synopsis

Rerun a failed or stopped build's failed jobs, as Bamboo's "Rerun failed jobs"
does: same build key, same variables and commit. A build that is running or
succeeded is refused.

```
bam build rerun [<build>|<plan>|<preset>] [flags]
```

### Examples

```
  bam build rerun PROJ-BUILD-482
  bam build rerun --last --watch
  bam build rerun provision --branch feature-x --dry-run
```

### Options

```
  -b, --branch string          plan branch when a plan or preset is given
      --dry-run[=true|false]   show which build would rerun, restart nothing
  -h, --help[=true|false]      help for rerun
      --last[=true|false]      the last build bam triggered from this repository (the default for commands that only read)
      --notify[=true|false]    when the watched build finishes, ring the terminal bell and raise a desktop notification where the system has one
      --timeout duration       with --watch, stop watching after this long (the build keeps running)
  -w, --watch[=true|false]     follow the build and the builds it triggers until they finish
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

* [bam build](bam_build.md)	 - Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel

