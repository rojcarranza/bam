## bam server add

Add a server alias to the machine config, then log in

```
bam server add <alias> --url URL [flags]
```

### Examples

```
  bam server add work --url https://bamboo.example.com
  bam server add lab --url https://bamboo.lab.example --project LAB --project OPS
  bam server add work --url https://bamboo.example.com --force
```

### Options

```
      --force[=true|false]   replace an existing alias
  -h, --help[=true|false]    help for add
      --project strings      project key to scope navigation (repeatable)
      --url string           server URL, e.g. https://bamboo.example.com
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

* [bam server](bam_server.md)	 - Server aliases: add, list, rm, default

