## bam preset rm

Remove a run preset from every file that defines it

### Synopsis

Remove a run preset from every file that defines it.
On a terminal, bam preset rm with no name opens the list of presets to pick from, and asks
before it removes one; --yes skips the question. With a pipe, --json, --no-tui or
BAM_NO_TUI, bam never asks: rm NAME removes at once and rm alone is a usage error.
A preset that a set uses is kept.

```
bam preset rm [name] [flags]
```

### Examples

```
  bam preset rm
  bam preset rm deploy-qa
  bam preset rm deploy-qa --yes
```

### Options

```
  -h, --help[=true|false]   help for rm
      --yes[=true|false]    do not ask first
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

* [bam preset](bam_preset.md)	 - Run presets: list, show, add, rm, sync, migrate

