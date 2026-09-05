package gh

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateAuth makes token resolution deterministic: no env token, an empty
// gh config dir, and GH_PATH naming a binary that does not exist, so
// go-gh's own `gh auth token --secure-storage` attempt fails fast instead
// of reading a real keyring. go-gh caches its config file once per
// process, so every test that resolves a token must start here.
func isolateAuth(t *testing.T) string {
	t.Helper()
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	bin := filepath.Join(t.TempDir(), "gh-does-not-exist")
	t.Setenv("GH_PATH", bin)
	return bin
}

// recordingRunner captures the subprocess request and answers with canned
// stdout or an error.
type recordingRunner struct {
	name  string
	args  []string
	calls int
	out   []byte
	err   error
}

func (r *recordingRunner) run(name string, args ...string) ([]byte, error) {
	r.calls++
	r.name, r.args = name, args
	return r.out, r.err
}

func TestTokenPrefersEnvOverSubprocess(t *testing.T) {
	isolateAuth(t)
	t.Setenv("GH_TOKEN", "env-token")
	r := &recordingRunner{out: []byte("sub-token\n")}
	got, err := token(r.run)
	if err != nil || got != "env-token" {
		t.Fatalf("token = %q, %v; want the env token", got, err)
	}
	if r.calls != 0 {
		t.Errorf("subprocess ran %d times with an env token present", r.calls)
	}
}

func TestTokenFallsBackToGhSubprocess(t *testing.T) {
	bin := isolateAuth(t)
	r := &recordingRunner{out: []byte("  keyring-token\n")}
	got, err := token(r.run)
	if err != nil || got != "keyring-token" {
		t.Fatalf("token = %q, %v; want the trimmed subprocess output", got, err)
	}
	if r.name != bin {
		t.Errorf("ran %q, want the GH_PATH binary %q", r.name, bin)
	}
	if got, want := strings.Join(r.args, " "), "auth token --hostname github.com"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// gh ran and refused: its own message is the useful one and must reach
// the user verbatim.
func TestTokenSurfacesGhRefusal(t *testing.T) {
	isolateAuth(t)
	r := &recordingRunner{err: &exec.ExitError{Stderr: []byte("not logged in to github.com\n")}}
	_, err := token(r.run)
	if err == nil || err.Error() != "gh auth token: not logged in to github.com" {
		t.Fatalf("err = %v, want gh's stderr wrapped", err)
	}
}

func TestTokenReportsMissingCredentials(t *testing.T) {
	cases := []struct {
		name string
		r    *recordingRunner
	}{
		{"empty stdout", &recordingRunner{out: []byte("\n")}},
		{"gh could not run", &recordingRunner{err: errors.New("exec: not found")}},
		{"exit without stderr", &recordingRunner{err: &exec.ExitError{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateAuth(t)
			_, err := token(c.r.run)
			if err == nil || !strings.Contains(err.Error(), "no GitHub credentials found") {
				t.Fatalf("err = %v, want the no-credentials message", err)
			}
			if c.r.calls != 1 {
				t.Errorf("subprocess ran %d times, want 1", c.r.calls)
			}
		})
	}
}
