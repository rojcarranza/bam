## bam open

Open a project, plan, build or job in the browser

```
bam open [<key>|<preset>] [flags]
```

### Examples

```
  bam open PROJ-BUILD
  bam open PROJ-BUILD-482 --job JOB1
  bam open --last
  bam open provision
```

### Options

```
  -h, --help[=true|false]   help for open
      --job string          a job of the plan or build, by short key (JOB1)
      --last[=true|false]   the last build bam triggered from this repository (the default for commands that only read)
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

