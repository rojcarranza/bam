Output gifs/preset-sync.gif
Output text/preset-sync.txt
Set Height 640
Source tapes/_common.tape
Source tapes/_start.tape
Hide
Type "mkdir -p ~/bam/presets && cp /demo/docs/demos/fixtures/stale-preset.yaml ~/bam/presets/setlist.yaml && clear"
Enter
Show
Type "bam preset show deploy"
Enter
Wait
Sleep 2s
Type "bam preset sync deploy --dry-run"
Enter
Wait@30s
Sleep 3s

Type "bam preset sync deploy"
Enter
Wait@30s
Sleep 3s

Type "bam preset show deploy"
Enter
Wait@30s
Sleep 3s
