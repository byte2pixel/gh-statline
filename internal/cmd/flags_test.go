package cmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/byte2pixel/gh-statline/internal/gh"
)

// --config and --db outrank STATLINE_CONFIG and STATLINE_DB, the way a gh
// flag outranks its environment variable, and they reach every subcommand
// through the root's persistent pre-run. doctor is the probe because it
// prints both paths and opens the cache without touching GitHub.
func TestConfigAndDBFlagsOutrankEnv(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "other.yml")
	dbPath := filepath.Join(dir, "other.db")

	// Put the config where only the flag will look. The env path isolate
	// chose stays empty, so a lookup that ignored the flag fails loudly.
	envCfg := os.Getenv("STATLINE_CONFIG")
	t.Setenv("STATLINE_CONFIG", cfgPath)
	writeConfig(t, testConfig())
	t.Setenv("STATLINE_CONFIG", envCfg)

	out, err := runCmd(t, "doctor", "--config", cfgPath, "--db", dbPath)
	if err != nil {
		t.Fatalf("err = %v:\n%s", err, out)
	}
	for _, want := range []string{cfgPath, dbPath} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor did not report the flagged path %s:\n%s", want, out)
		}
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("cache was not opened at --db: %v", err)
	}
	if _, err := os.Stat(os.Getenv("STATLINE_DB")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("cache was also created at the env path, stat err = %v", err)
	}
}

// --version answers the same line as the version subcommand, and answers
// before any client or config is touched: a version check has to work on a
// machine with no gh login and no config file, which is where bug reports
// start.
func TestVersionFlagMatchesSubcommand(t *testing.T) {
	isolate(t)
	newClient = func() (gh.Doer, error) {
		t.Fatal("--version built a GitHub client")
		return nil, nil
	}
	flag, err := runCmd(t, "--version")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := runCmd(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if flag != sub {
		t.Errorf("--version printed %q, version printed %q; want the same line", flag, sub)
	}
	if !strings.HasPrefix(flag, "statline ") || strings.TrimSpace(flag) == "statline" {
		t.Errorf("--version printed %q, want statline <version>", flag)
	}
}
