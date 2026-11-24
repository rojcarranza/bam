# demo: in-repo
# demo: config
Output gifs/completion.gif
Output text/completion.txt
Set Height 560
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Hide
Type ". /usr/share/bash-completion/bash_completion && clear"
Enter
Show
Type "source <(bam completion bash)"
Enter
Sleep 1s
Type "bam ru"
Tab
Sleep 800ms
Tab
Tab
Sleep 2s
Type "deploy"
Tab
Sleep 800ms
Type "--var "
Tab
Tab
Sleep 2.5s
Ctrl+C
Type "bam build show SETLIST-"
Tab
Tab
Sleep 3s
Ctrl+C
