# demo: no-login
Output gifs/connect.gif
Output text/connect.txt
Set Height 560
Source tapes/_common.tape
Source tapes/_start.tape
Type "bam server add work --url https://bamboo.example.com --project SETLIST"
Enter
Sleep 1.5s
Hide
Type "{{DEMO_TOKEN}}"
Show
Enter
Sleep 2s
Type "bam doctor"
Enter
Sleep 4s
