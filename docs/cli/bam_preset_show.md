## bam preset show

Show a run preset with its defaults, options and origin

```
bam preset show <name> [flags]
```

### Examples

```
  bam preset show provision
  bam preset show provision --json
```

### Options

```
  -h, --help[=true|false]   help for show
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

