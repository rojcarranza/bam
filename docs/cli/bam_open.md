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
  -h, --help         help for open
      --job string   a job of the plan or build, by short key (JOB1)
      --last         the last build bam triggered from this repository
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

