// Package gh provides GitHub authentication, the GraphQL client, and the
// query documents Statline runs against the GitHub API.
package gh

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/safeexec"
)

const githubHost = "github.com"

// host is the GitHub instance to talk to, resolved the way gh resolves it:
// GH_HOST, else the one host gh is logged in to, else github.com. Both the
// token lookup and the API endpoint read it, so statline finds a gh that
// holds credentials only for an Enterprise Server instead of reporting
// none. The queries are tested against github.com alone, so any other host
// is best effort.
func host() string {
	h, _ := auth.DefaultHost()
	return h
}

// ghPath locates the gh executable. gh exports GH_PATH when it runs an
// extension, which is exact; otherwise search PATH through safeexec, which
// refuses a match in the current directory. A plain PATH lookup would let a
// gh planted in the working directory receive the user's credentials.
func ghPath() (string, error) {
	if p := os.Getenv("GH_PATH"); p != "" {
		return p, nil
	}
	return safeexec.LookPath("gh")
}

// runner executes a subprocess and returns its stdout: the one seam for
// the `gh auth token` fallback, so tests never spawn a real gh.
type runner func(name string, args ...string) ([]byte, error)

func execRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// Token resolves a GitHub token: go-gh's resolution (GH_TOKEN/GITHUB_TOKEN
// env, gh's config file, then its own silent `gh auth token` attempt at the
// keyring), and failing all of that our own `gh auth token` subprocess,
// which is what surfaces gh's reason when the keyring token is missing or
// expired. The token arrives on stdout, so it never appears in the process
// list.
func Token() (string, error) { return token(execRunner) }

func token(run runner) (string, error) {
	h := host()
	if t, _ := auth.TokenForHost(h); t != "" {
		return t, nil
	}
	if bin, err := ghPath(); err == nil {
		out, err := run(bin, "auth", "token", "--hostname", h)
		if err == nil {
			if t := strings.TrimSpace(string(out)); t != "" {
				return t, nil
			}
		} else if msg := stderrOf(err); msg != "" {
			// gh ran and refused. Its own message says why, and swallowing
			// it leaves the user guessing at "no credentials found".
			return "", fmt.Errorf("gh auth token: %s", msg)
		}
	}
	return "", noCredentials(h)
}

// noCredentials names the host whenever it is not github.com. A user whose
// gh points at an Enterprise Server cannot guess which host statline
// searched, and the env var that works there is GH_ENTERPRISE_TOKEN.
// go-gh reads GITHUB_TOKEN for github.com, tenancy (*.ghe.com) and
// localhost only, the same split auth.IsEnterprise makes.
func noCredentials(h string) error {
	if h == githubHost {
		return errors.New("no GitHub credentials found — run 'gh auth login' or set GITHUB_TOKEN")
	}
	envVar := "GITHUB_TOKEN"
	if auth.IsEnterprise(h) {
		envVar = "GH_ENTERPRISE_TOKEN"
	}
	return fmt.Errorf("no GitHub credentials found for %s — run 'gh auth login --hostname %s' or set %s",
		h, h, envVar)
}

func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return strings.TrimSpace(string(exit.Stderr))
	}
	return ""
}
