## bam login

Store a verified personal access token for a server

### Synopsis

Store a verified personal access token for a server. The alias defaults to --server.

```
bam login <alias> [flags]
```

### Examples

```
  bam login work
  bam login --server work
  printf '%s' "$TOKEN" | bam login work --with-token
```

### Options

```
  -h, --help[=true|false]         help for login
      --with-token[=true|false]   read the token from stdin instead of prompting
```

### Options inherited from parent commands

```
      --color string          color output: auto, always or never
      --debug[=true|false]    log HTTP requests to stderr
      --json[=true|false]     print JSON
      --no-tui[=true|false]   never open the terminal UI
      --server string         server alias to use
```

### SEE ALSO

* [bam](bam.md)	 - A terminal remote control for Atlassian Bamboo

