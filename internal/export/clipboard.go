package export

import (
	"context"
	"time"

	"github.com/gopasspw/clipboard"
)

// ToClipboard writes text to the system clipboard. Returns false when no
// clipboard mechanism is available (headless Linux without xclip/wl-copy);
// callers should then fall back to OSC52 via Bubble Tea.
func ToClipboard(text string) (bool, error) {
	if clipboard.IsUnsupported() {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := clipboard.WriteAllString(ctx, text); err != nil {
		return false, err
	}
	return true, nil
}
