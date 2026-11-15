# demo: in-repo
# demo: config
# demo: it-next=pass
Output gifs/status.gif
Output text/status.txt
Set Height 460
Set PlaybackSpeed 2
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam run ci"
Enter
Wait@90s
Sleep 1s

Type "bam status"
Enter
Wait@30s
Sleep 2s

Type "bam status --short"
Enter
Wait@30s
Sleep 2s
