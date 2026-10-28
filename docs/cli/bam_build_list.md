## bam build list

List recent builds of a plan

```
bam build list <plan|preset> [flags]
```

### Options

```
      --branch string   plan branch
  -h, --help            help for list
  -n, --limit int       number of builds (default 10)
      --state string    only builds in this state, e.g. failed
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

