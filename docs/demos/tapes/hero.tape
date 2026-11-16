# demo: in-repo
# demo: config
# demo: branch
Output gifs/hero.gif
Output text/hero.txt
Set Height 720
Set PlaybackSpeed 3
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam run ci --branch @"
Enter
Wait@120s
Sleep 2s

Type "bam logs --last --failed -n 10"
Enter
Wait@30s
Sleep 3s

Type "bam open --last"
Enter
Wait@30s
Sleep 3s
