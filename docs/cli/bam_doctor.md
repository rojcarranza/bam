## bam doctor

Check config, credentials and what this Bamboo server supports

```
bam doctor [flags]
```

### Examples

```
  bam doctor
  bam doctor --server lab
  bam doctor --plan PROJ-BUILD
```

### Options

```
  -h, --help          help for doctor
      --plan string   plan to probe (default: first plan of the first configured project)
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

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo

