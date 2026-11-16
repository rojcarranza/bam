# demo: in-repo
# demo: config
Output gifs/pin.gif
Output text/pin.txt
Set Height 460
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam pin add release SETLIST-CI SETLIST-DEPLOY SETLIST-SMOKE"
Enter
Wait@30s
Sleep 2s

Type "bam pin list"
Enter
Wait@30s
Sleep 3s
