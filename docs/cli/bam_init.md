## bam init

Write .bam.yaml for this repository, with generated presets

```
bam init --server ALIAS --project KEY [--plan KEY[=name]]... [flags]
```

### Options

```
      --force              overwrite an existing .bam.yaml
  -h, --help               help for init
      --plan stringArray   plan to generate a preset from, KEY or KEY=name (repeatable)
      --project strings    project key (repeatable; the first is the default)
      --url string         server URL (default: the URL of --server in the machine config)
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

