# demo: in-repo
# demo: config
# demo: idle
Output gifs/set-run.gif
Output text/set-run.txt
Set Height 900
Set PlaybackSpeed 4
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam set show release"
Enter
Wait@30s
Sleep 2s

Type "bam set run release --dry-run"
Enter
Wait@30s
Sleep 3s

Type "bam set run release"
Enter
Wait@300s
Sleep 4s
