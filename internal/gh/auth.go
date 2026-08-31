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

const host = "github.com"

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

// Token resolves a GitHub token: go-gh's resolution (GH_TOKEN/GITHUB_TOKEN
// env, then gh's config file), then a `gh auth token` subprocess — needed
// when gh stores the token in the OS keyring, which go-gh cannot read. The
// token arrives on stdout, so it never appears in the process list.
func Token() (string, error) {
	if t, _ := auth.TokenForHost(host); t != "" {
		return t, nil
	}
	if bin, err := ghPath(); err == nil {
		out, err := exec.Command(bin, "auth", "token", "--hostname", host).Output()
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
	return "", errors.New("no GitHub credentials found — run 'gh auth login' or set GITHUB_TOKEN")
}

func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return strings.TrimSpace(string(exit.Stderr))
	}
	return ""
}
