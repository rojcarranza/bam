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
      --all[=true|false]    list every project on the server
  -h, --help[=true|false]   help for list
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

* [bam project](bam_project.md)	 - Projects: list

