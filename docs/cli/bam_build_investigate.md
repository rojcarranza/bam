## bam build investigate

Find the error lines of a failed build and its likely cause

```
bam build investigate [<build>|<plan>|<preset>] [flags]
```

### Examples

```
  bam build investigate PROJ-BUILD-482
  bam build investigate --last --compare
  bam build investigate provision --context 0 --max 0
```

### Options

```
  -b, --branch string          plan branch when a plan or preset is given
      --compare[=true|false]   show revisions and variables changed since the last successful build
      --context int            lines of context around each error (default 3)
  -h, --help[=true|false]      help for investigate
      --last[=true|false]      the last build bam triggered from this repository (the default for commands that only read)
      --max int                error blocks per job; 0 for all (default 5)
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

