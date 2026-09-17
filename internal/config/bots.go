package config

import (
	"regexp"
	"strings"
	"sync"
)

// BotMatcher reports whether a login matches the exclude_bots globs.
// Globs support * (any run) and ? (any single char); all other characters
// are literal — notably [ and ], since patterns like "*[bot]" must match
// the literal brackets GitHub puts in bot logins (path.Match would treat
// them as a character class).
type BotMatcher struct {
	res []*regexp.Regexp

	mu    sync.Mutex
	cache map[string]bool
}

func NewBotMatcher(globs []string) *BotMatcher {
	m := &BotMatcher{cache: map[string]bool{}}
	for _, g := range globs {
		m.res = append(m.res, globToRegexp(g))
	}
	return m
}

func (m *BotMatcher) IsBot(login string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.cache[login]; ok {
		return v
	}
	v := false
	for _, re := range m.res {
		if re.MatchString(login) {
			v = true
			break
		}
	}
	m.cache[login] = v
	return v
}

func globToRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// SQLGlobs translates the exclude_bots globs into SQLite GLOB patterns for
// the bot_actors view, which matches them against lower(login). The two
// syntaxes share * and ?; the pattern is lowercased because GLOB is
// case-sensitive, and [ becomes the class [[] because GLOB would otherwise
// read "*[bot]" as a character class. GitHub logins are ASCII, so the ASCII
// lower() in SQLite folds exactly what the (?i) in globToRegexp does;
// config/sqlglob_test.go pins the two against each other.
func SQLGlobs(globs []string) []string {
	out := make([]string, 0, len(globs))
	for _, g := range globs {
		out = append(out, strings.ReplaceAll(strings.ToLower(g), "[", "[[]"))
	}
	return out
}
