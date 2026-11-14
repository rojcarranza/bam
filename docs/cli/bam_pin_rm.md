## bam pin rm

Remove a pin from the machine file

### Synopsis

Remove a pin from the monitor: section of the machine file. A pin that lives in
.bam.yaml is edited there by hand.

```
bam pin rm PIN [flags]
```

### Examples

```
  bam pin rm PROJ-BUILD
```

### Options

```
  -h, --help[=true|false]   help for rm
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

* [bam pin](bam_pin.md)	 - Pins: add, rm, list

