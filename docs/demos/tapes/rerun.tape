# demo: in-repo
# demo: config
# demo: it-next=fail
Output gifs/rerun.gif
Output text/rerun.txt
Set Height 620
Set PlaybackSpeed 3
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

Type "bam build tree --last --failed"
Enter
Wait@30s
Sleep 2s

Type "bam build tree --last --rerun-failed --yes"
Enter
Wait@90s
Sleep 3s
