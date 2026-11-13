## bam agent list

List the build agents: online, enabled and busy

```
bam agent list [flags]
```

### Examples

```
  bam agent list
  bam agent list --mine
  bam agent list --json
```

### Options

```
  -h, --help[=true|false]   help for list
      --mine[=true|false]   list only the agents whose names start with one you listed (bam agent mine)
```

### Options inherited from parent commands

```
      --color string          color output: auto, always or never
      --debug[=true|false]    log HTTP requests to stderr
      --json[=true|false]     print JSON
      --no-tui[=true|false]   never open the terminal UI
      --server string         server alias to use
```

### SEE ALSO

* [bam agent](bam_agent.md)	 - Build agents: list, mine

