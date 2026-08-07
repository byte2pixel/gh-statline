package config

import (
	"os"
	"path/filepath"
)

const appDir = "gh-statline"

// FilePath returns the config file location, honoring STATLINE_CONFIG for
// tests and power users. Windows: %AppData%\gh-statline\config.yml;
// Linux/macOS: ~/.config/gh-statline/config.yml (per os.UserConfigDir).
func FilePath() (string, error) {
	if p := os.Getenv("STATLINE_CONFIG"); p != "" {
		return p, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDir, "config.yml"), nil
}

// DBPath returns the SQLite cache location. The DB is a rebuildable cache,
// so it lives under the user cache dir, not next to the config.
func DBPath() (string, error) {
	if p := os.Getenv("STATLINE_DB"); p != "" {
		return p, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDir, "statline.db"), nil
}
