## bam url

Print the Bamboo URL of a project, plan, build or job

```
bam url [<key>|<preset>] [flags]
```

### Examples

```
  bam url PROJ
  bam url PROJ-BUILD-482
  bam url --last --job JOB1
  bam url provision --json
```

### Options

```
  -h, --help         help for url
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

