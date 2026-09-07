package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

// ErrNotFound signals that no config file exists yet — the caller should
// route to the first-run wizard rather than fail.
var ErrNotFound = errors.New("config file not found")

func Load() (Config, error) {
	path, err := FilePath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return c, nil
}

func Save(c Config) error {
	// Load validates, so writing a config that fails Validate trades a
	// reported failure here for a hard startup failure on the next run,
	// after the state that caused it is long gone. Refuse instead: the
	// caller surfaces the error and the session keeps its in-memory value.
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to write an invalid config: %w", err)
	}
	path, err := FilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// writeAtomic writes data to path via a temp file and a rename. The rename
// itself is atomic, but only over content the filesystem has committed:
// without the fsync, a power loss between the write and the rename can
// publish a zero-length config. The parent directory is not synced, so a
// crash can still lose the write entirely — what it cannot do is leave a
// half-written config in its place.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	// A short write needs no separate check: os.File.Write reports
	// io.ErrShortWrite whenever it writes less than all of data, which is the
	// same guarantee the os.WriteFile call this replaced relied on.
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
