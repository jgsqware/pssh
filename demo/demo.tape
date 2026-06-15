# Regenerate a real GIF demo with charmbracelet/vhs:  vhs demo/demo.tape
# (needs vhs + ttyd + ffmpeg). For an honest recording without a live Passbolt
# server, point pssh at the docker test's fake stub + a fixture ssh config.
Output demo/demo.gif

Set FontSize 16
Set Width 1100
Set Height 560
Set Padding 20
Set Theme "Catppuccin Mocha"

Type "pssh app-prod-1"
Sleep 600ms
Enter
Sleep 3s
