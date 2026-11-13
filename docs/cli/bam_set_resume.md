## bam set resume

Continue an interrupted or timed-out set run

```
bam set resume [set|id] [flags]
```

### Examples

```
  bam set resume
  bam set resume release
  bam set resume release-20260929T101500Z --var version=1.4.0
```

### Options

```
      --file string          the train file of a file set, when it has moved since the run
      --force[=true|false]   take over the lock of a set run whose process is gone
  -h, --help[=true|false]    help for resume
      --var stringArray      variable name=value; give again every --var the run was started with (repeatable)
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

