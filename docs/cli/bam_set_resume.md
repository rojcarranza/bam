## bam set resume

Continue an interrupted or timed-out set run

```
bam set resume [set|id] [flags]
```

### Options

```
      --force             take over the lock of a set run whose process is gone
  -h, --help              help for resume
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

