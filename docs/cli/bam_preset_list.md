## bam preset list

List run presets from your presets files, .bam.yaml and the machine config

### Synopsis

List run presets from your presets files, .bam.yaml and the machine config.
With text, list only the presets whose name or plan contains it, ignoring case.

```
bam preset list [text] [flags]
```

### Examples

```
  bam preset list
  bam preset list prov
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

* [bam preset](bam_preset.md)	 - Run presets: list, show, add, rm, sync, migrate

