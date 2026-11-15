# demo: in-repo
# demo: config
# demo: branch
Output gifs/feature-branch.gif
Output text/feature-branch.txt
Set Height 720
Set PlaybackSpeed 3
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "git branch --show-current"
Enter
Wait@30s
Sleep 1s

Type "bam run ci --branch @"
Enter
Wait@120s
Sleep 2s

Type "bam build investigate --last"
Enter
Wait@30s
Sleep 4s
