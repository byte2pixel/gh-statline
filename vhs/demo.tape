# README demo, rendered against the seeded no-sync "demo" team so no real
# data ever appears. Regenerate with Docker (no local vhs install needed):
#
#   DEMO=$(mktemp -d)
#   mkdir -p "$DEMO/bin" "$DEMO/demo"
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$DEMO/demo/statline" .
#   STATLINE_CONFIG="$DEMO/demo/config.yml" STATLINE_DB="$DEMO/demo/cache.db" go run . seed
#   printf '#!/bin/sh\nexport STATLINE_CONFIG=/vhs/demo/config.yml\nexport STATLINE_DB=/vhs/demo/cache.db\nexport GITHUB_TOKEN=demo-no-sync\nexec /vhs/demo/statline\n' > "$DEMO/bin/gh"
#   chmod +x "$DEMO/bin/gh"
#   cp vhs/demo.tape "$DEMO/"
#   docker run --rm -v "$DEMO:/vhs" ghcr.io/charmbracelet/vhs demo.tape
#   cp "$DEMO/demo.gif" .github/demo.gif

Output demo.gif

Set FontSize 16
Set Width 1200
Set Height 680
Set Padding 16
Set TypingSpeed 60ms

Hide
Type "export PATH=/vhs/bin:$PATH && clear"
Enter
Show

Type "gh statline"
Sleep 500ms
Enter
Sleep 3.5s

# Team stats: move the selection, flip through a sort column.
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "l"
Sleep 1.5s

# Charts: let the bars spring in.
Type "2"
Sleep 4s

# Trends, then fullscreen the first card for the per-member breakdown.
Type "3"
Sleep 3s
Type "f"
Sleep 2s
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 1.5s
Escape
Sleep 1s

# Land back on the team table.
Type "1"
Sleep 2.5s
