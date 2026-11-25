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

* [bam preset](bam_preset.md)	 - Run presets: list, show, add, edit, rm, sync, migrate

