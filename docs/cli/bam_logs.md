## bam logs

Print job logs; --failed for failed jobs only

```
bam logs [<build>|<plan>|<preset>] [flags]
```

### Examples

```
  bam logs PROJ-BUILD-482 --failed
  bam logs --last --failed -n 50
  bam logs PROJ-BUILD-482 --job JOB1 --follow
  bam logs PROJ-BUILD-JOB1-482
```

### Options

```
  -b, --branch string         plan branch when a plan or preset is given
      --failed[=true|false]   only failed jobs
      --follow[=true|false]   keep printing new lines until the job finishes
  -h, --help[=true|false]     help for logs
      --job string            one job: JOB1, PROJ-PLAN-JOB1 or its result key
      --last[=true|false]     the last build bam triggered from this repository (the default for commands that only read)
  -n, --tail int              last N lines per job; 0 prints the whole log
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

