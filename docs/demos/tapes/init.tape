# demo: in-repo
Output gifs/init.gif
Output text/init.txt
Set Height 720
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "cd ~/setlist-service && clear"
Enter
Show
Type "bam init --server work --project SETLIST --plan SETLIST-CI=ci --plan SETLIST-DEPLOY=deploy --plan SETLIST-SMOKE=smoke"
Enter
Wait@30s
Sleep 2s

Type "head -14 .bam.yaml"
Enter
Wait@30s
Sleep 2s

Type "bam preset list"
Enter
Wait@30s
Sleep 3s
