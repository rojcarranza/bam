## bam usage

Show how often you ran each command, or turn counting off or on

### Synopsis

bam keeps a private tally on this computer of which commands you run and how often,
by day. It holds command names and counts only, never arguments or the names of
plans, servers, jobs, agents or branches, and bam sends it nowhere.

bam usage        shows the tally
bam usage off    stops counting (the counts so far stay)
bam usage on     counts again

```
bam usage [off|on] [flags]
```

### Options

```
  -h, --help[=true|false]   help for usage
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

