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
  -h, --help   help for status
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

