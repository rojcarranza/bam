# demo: in-repo
# demo: config
# demo: idle
Output gifs/cancel.gif
Output text/cancel.txt
Set Height 620
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam run load --watch=false"
Enter
Wait@20s
Sleep 2s

Type "bam build cancel --last --dry-run"
Enter
Wait@30s
Sleep 2s

Type "bam build cancel --last"
Enter
Wait@30s
Sleep 3s

Type "bam build show --last"
Enter
Wait@30s
Sleep 3s
