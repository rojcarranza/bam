## bam watch

Follow a build until it finishes; exit 1 if it fails

```
bam watch [<build>|<plan>|<preset>] [flags]
```

### Options

```
      --branch string      plan branch when a plan or preset is given
  -h, --help               help for watch
      --last               the last build bam triggered from this repository
      --timeout duration   stop watching after this long (the build keeps running)
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

