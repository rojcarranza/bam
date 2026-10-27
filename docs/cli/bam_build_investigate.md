## bam build investigate

Find the error lines of a failed build and its likely cause

```
bam build investigate [<build>|<plan>|<preset>] [flags]
```

### Options

```
      --branch string   plan branch when a plan or preset is given
      --compare         show revisions and variables changed since the last successful build
      --context int     lines of context around each error (default 3)
  -h, --help            help for investigate
      --last            the last build bam triggered from this repository
      --max int         error blocks per job; 0 for all (default 5)
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

