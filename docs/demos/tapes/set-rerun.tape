# demo: in-repo
# demo: config
# demo: it-next=fail
Output gifs/set-rerun.gif
Output text/set-rerun.txt
Set Height 560
Set PlaybackSpeed 2.5
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam set run verify"
Enter
Wait@120s
Sleep 2s

Type "bam set rerun --failed"
Enter
Wait@120s
Sleep 4s
