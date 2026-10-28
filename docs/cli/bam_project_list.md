## bam project list

List configured projects (--all: every project on the server)

### Synopsis

List configured projects (--all: every project on the server). With text, list
only the projects whose key or name contains it, ignoring case.

```
bam project list [text] [flags]
```

### Examples

```
  bam project list
  bam project list --all ops
```

### Options

```
      --all    list every project on the server
  -h, --help   help for list
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

* [bam project](bam_project.md)	 - Projects: list

