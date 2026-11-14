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
  -h, --help[=true|false]   help for list
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

* [bam set](bam_set.md)	 - Release trains: presets run in ordered stages

