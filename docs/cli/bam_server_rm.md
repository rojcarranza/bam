## bam server rm

Remove a server alias from the machine config

```
bam server rm <alias> [flags]
```

### Examples

```
  bam server rm lab
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

* [bam server](bam_server.md)	 - Server aliases: add, list, rm, default

