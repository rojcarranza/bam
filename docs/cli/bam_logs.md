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
      --branch string   plan branch when a plan or preset is given
      --failed          only failed jobs
      --follow          keep printing new lines until the job finishes
  -h, --help            help for logs
      --job string      one job: JOB1, PROJ-PLAN-JOB1 or its result key
      --last            the last build bam triggered from this repository
  -n, --tail int        last N lines per job; 0 prints the whole log
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

