# demo: in-repo
# demo: config
# demo: it-next=fail
Output gifs/tree.gif
Output text/tree.txt
Set Height 560
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
Sleep 2s

Type "bam build tree --last"
Enter
Wait@30s
Sleep 2s

Type "bam build tree --last --failed"
Enter
Wait@30s
Sleep 3s
