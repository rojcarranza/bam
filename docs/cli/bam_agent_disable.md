## bam agent disable

Disable a build agent (administrators only)

### Synopsis

Take an agent out of rotation: Bamboo stops giving it builds. A build the agent is
running now finishes first. Only a Bamboo administrator can do it. bam asks first;
without a terminal, or with --json, it needs --yes. Undo it with bam agent enable NAME.

```
bam agent disable NAME [flags]
```

### Examples

```
  bam agent disable linux-3
  bam agent disable linux-3 --yes
```

### Options

```
  -h, --help[=true|false]   help for disable
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

* [bam agent](bam_agent.md)	 - Build agents: list, mine, disable, enable

