package gh

import (
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestIsRetryable(t *testing.T) {
	httpErr := func(code int) error {
		return &api.HTTPError{StatusCode: code, RequestURL: nil, Message: "x"}
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"server error 500", httpErr(500), true},
		{"bad gateway 502", httpErr(502), true},
		{"rate limited 403", httpErr(http.StatusForbidden), true},
		{"secondary limit 429", httpErr(http.StatusTooManyRequests), true},
		{"not found 404", httpErr(http.StatusNotFound), false},
		{"unauthorized 401", httpErr(http.StatusUnauthorized), false},
		{"semantic graphql error", &api.GraphQLError{}, false},
		{"transport error", errors.New("connection reset"), true},
		{"wrapped http error", errors.Join(errors.New("ctx"), httpErr(503)), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRetryable(c.err); got != c.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// gh exports GH_PATH when it runs an extension. Preferring it avoids a PATH
// search entirely, so the token cannot be handed to a different gh.
func TestGhPathPrefersEnv(t *testing.T) {
	t.Setenv("GH_PATH", filepath.Join("some", "dir", "gh"))
	got, err := ghPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("some", "dir", "gh"); got != want {
		t.Errorf("ghPath = %q, want %q", got, want)
	}
}

func TestStderrOfExitError(t *testing.T) {
	if got := stderrOf(errors.New("plain")); got != "" {
		t.Errorf("non-exit error returned %q, want empty", got)
	}
	exit := &exec.ExitError{Stderr: []byte("  not logged in\n")}
	if got := stderrOf(exit); got != "not logged in" {
		t.Errorf("stderrOf = %q, want \"not logged in\"", got)
	}
}

func TestActorSafeLoginAndIsBot(t *testing.T) {
	var ghost *Actor
	if ghost.SafeLogin() != "ghost" {
		t.Errorf("nil actor SafeLogin = %q, want ghost", ghost.SafeLogin())
	}
	if ghost.IsBot() {
		t.Error("nil actor reported as bot")
	}
	empty := &Actor{}
	if empty.SafeLogin() != "ghost" {
		t.Errorf("empty-login actor SafeLogin = %q, want ghost", empty.SafeLogin())
	}
	bot := &Actor{Login: "dependabot", TypeName: "Bot"}
	if !bot.IsBot() || bot.SafeLogin() != "dependabot" {
		t.Errorf("bot actor: IsBot=%v SafeLogin=%q", bot.IsBot(), bot.SafeLogin())
	}
	user := &Actor{Login: "alice", TypeName: "User"}
	if user.IsBot() {
		t.Error("user actor reported as bot")
	}
	// Every login-less actor is stored as "ghost", and users.is_bot is
	// last-write-wins per login: flagging one would drop every deleted
	// account's reviews and comments from the numbers at once.
	anon := &Actor{TypeName: "Bot"}
	if anon.IsBot() {
		t.Error("login-less bot actor must not flag the shared ghost row")
	}
}
