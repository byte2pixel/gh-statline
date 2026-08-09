# Social preview card (1280×640 — GitHub's recommended size) rendered from
# the seeded demo team. Same sandbox setup as demo.tape; regenerate with:
#
#   cp vhs/social.tape "$DEMO/"
#   docker run --rm -v "$DEMO:/vhs" ghcr.io/charmbracelet/vhs social.tape
#   docker run --rm -v "$DEMO:/vhs" --entrypoint ffmpeg ghcr.io/charmbracelet/vhs \
#     -sseof -0.1 -i /vhs/social-scratch.gif -update 1 -frames:v 1 /vhs/social-preview.png -y
#   cp "$DEMO/social-preview.png" .github/social-preview.png
#
# then upload .github/social-preview.png in Settings → Social preview.
# (ffmpeg pulls the final frame; this vhs build ignores the Screenshot
# command and Output only accepts gif/mp4/webm.)

Output social-scratch.gif

Set FontSize 16
Set Width 1280
Set Height 640
Set Padding 20

Hide
Type "export PATH=/vhs/bin:$PATH && clear"
Enter
Type "gh statline"
Enter
Sleep 3s
Type "2"
Sleep 4s
Show
Sleep 1s
