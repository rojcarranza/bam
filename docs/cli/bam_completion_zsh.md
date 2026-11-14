## bam completion zsh

Generate the autocompletion script for zsh

### Synopsis

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(bam completion zsh)

To load completions for every new session, execute once:

#### Linux:

	bam completion zsh > "${fpath[1]}/_bam"

#### macOS:

	bam completion zsh > $(brew --prefix)/share/zsh/site-functions/_bam

You will need to start a new shell for this setup to take effect.


```
bam completion zsh [flags]
```

### Options

```
  -h, --help[=true|false]              help for zsh
      --no-descriptions[=true|false]   disable completion descriptions
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

* [bam completion](bam_completion.md)	 - Generate the autocompletion script for the specified shell

