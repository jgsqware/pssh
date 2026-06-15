# Regenerate the pg-plugin GIF with charmbracelet/vhs:  vhs demo/pg.tape
# (needs vhs + ttyd + ffmpeg). Point pssh at a host whose Docker containers
# expose a *ConnectionString* env var; pick one and it tunnels + opens lazysql.
Output demo/pg.gif

Set FontSize 16
Set Width 1100
Set Height 600
Set Padding 20
Set Theme "Catppuccin Mocha"

Type "pssh pg app-prod-1"
Sleep 600ms
Enter
Sleep 2s
Type "orders"
Sleep 500ms
Enter
Sleep 3s
