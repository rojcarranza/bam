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
  -h, --help   help for rm
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

* [bam pin](bam_pin.md)	 - Pins: add, rm, list

