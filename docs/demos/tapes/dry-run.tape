# demo: in-repo
# demo: config
Output gifs/dry-run.gif
Output text/dry-run.txt
Set Height 520
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam run deploy --var version=1.0.2 --var environment=staging --dry-run"
Enter
Wait@30s
Sleep 3s

Type "bam run deploy --var version=1.0.2 --var environment=qa --dry-run"
Enter
Wait@30s
Sleep 3s

Type "bam run deploy --dry-run"
Enter
Wait@30s
Sleep 3s
