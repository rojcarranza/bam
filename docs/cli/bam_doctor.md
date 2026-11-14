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
  -h, --help[=true|false]   help for doctor
      --plan string         plan to probe (default: first plan of the first configured project)
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

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo

