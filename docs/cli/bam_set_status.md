## bam set status

Show a set run and the current state of its builds

```
bam set status [set|id] [flags]
```

### Examples

```
  bam set status
  bam set status release
  bam set status release-20260929T101500Z
```

### Options

```
  -h, --help[=true|false]   help for status
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

