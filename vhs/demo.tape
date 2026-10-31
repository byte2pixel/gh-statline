# README demo, rendered against the seeded no-sync "demo" team so no real
# data ever appears. Regenerate with Docker (no local vhs install needed):
#
#   DEMO=$(mktemp -d)
#   mkdir -p "$DEMO/bin" "$DEMO/demo"
#   GOOS=linux CGO_ENABLED=0 go build -o "$DEMO/demo/statline" .   # host GOARCH matches the Docker image platform
#   STATLINE_CONFIG="$DEMO/demo/config.yml" STATLINE_DB="$DEMO/demo/cache.db" go run . seed
#   printf '#!/bin/sh\nexport STATLINE_CONFIG=/vhs/demo/config.yml\nexport STATLINE_DB=/vhs/demo/cache.db\nexport GITHUB_TOKEN=demo-no-sync\nexport COLORTERM=truecolor\nexec /vhs/demo/statline\n' > "$DEMO/bin/gh"
#   (COLORTERM matters: without it the app degrades hex colors to a 256-color
#   palette — the status bar goes navy instead of its muted #2A2A38.)
#   chmod +x "$DEMO/bin/gh"
#   cp vhs/demo.tape "$DEMO/"
#   docker run --rm -v "$DEMO:/vhs" ghcr.io/charmbracelet/vhs demo.tape
#   cp "$DEMO/demo.gif" .github/demo.gif

Output demo.gif

Set FontSize 16
# 1280 is about 125 columns: the full team table (two #107 columns
# included) needs 118 with the demo team's longest login.
Set Width 1280
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

# Team stats: move the selection, step the sort from Merged onto the
# abandoned column (the status bar says what it counts), then drill into
# the member for the headline counts and timings. The sort persists to
# the config, so render from a fresh seed.
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "l"
Sleep 2.5s
Enter
Sleep 2.5s
Escape
Sleep 1s

# Charts: let the bars spring in, then fullscreen the review matrix and
# pan it — the labels stay pinned.
Type "2"
Sleep 4s
Type "j"
Sleep 500ms
Type "f"
Sleep 2s
Type "l"
Sleep 300ms
Type "l"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 1.5s
Escape
Sleep 1s

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
