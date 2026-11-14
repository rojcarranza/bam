## bam agent mine

The agents you support: add, rm, list, forget

### Synopsis

bam top shows the health of the agents you support, not the whole server's. Name
them by the start of their names: an agent is yours when its name starts with one
of the starts you list (case-sensitive, no wildcards). The list is agents: in the
machine file, which keeps its comments; it is also read from the project file, a
servers entry and a repos entry, wherever projects: is.

bam remembers the names of your agents it has seen, so an agent Bamboo drops from
its list still shows as down, and forgets a name that has been gone for 30 days.

```
bam agent mine [flags]
```

### Options

```
  -h, --help[=true|false]   help for mine
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
* [bam agent mine add](bam_agent_mine_add.md)	 - List name starts of the agents you support
* [bam agent mine forget](bam_agent_mine_forget.md)	 - Forget agents bam remembers, so they stop showing as down
* [bam agent mine list](bam_agent_mine_list.md)	 - List the name starts of the agents you support
* [bam agent mine rm](bam_agent_mine_rm.md)	 - Stop listing name starts

