## bam logs

Print job logs; --failed for failed jobs only

```
bam logs [<build>|<plan>|<preset>] [flags]
```

### Options

```
      --branch string   plan branch when a plan or preset is given
      --failed          only failed jobs
      --follow          keep printing new lines until the job finishes
  -h, --help            help for logs
      --job string      one job: JOB1, PROJ-PLAN-JOB1 or its result key
      --last            the last build bam triggered from this repository
      --tail int        last N lines per job
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

