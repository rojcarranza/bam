## bam set list

List release train sets

### Synopsis

List release train sets.
With text, list only the sets whose name or one of whose presets contains it, ignoring case.

```
bam set list [text] [flags]
```

### Examples

```
  bam set list
  bam set list prov
```

### Options

```
  -h, --help   help for list
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

