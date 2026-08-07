package wizard

import (
	"fmt"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

type reviewKind int

const (
	kindMember reviewKind = iota
	kindRepo
)

type reviewItem struct {
	kind     reviewKind
	label    string
	owner    string // repos only
	name     string // repos only
	included bool
}

// reviewList is the wizard's include/exclude toggle list over imported
// members and repos. Deliberately hand-rolled: bubbles' list doesn't do
// grouped sections with checkboxes.
type reviewList struct {
	theme  *theme.Theme
	items  []reviewItem
	cursor int
}

func newReviewList(th *theme.Theme, members []string, repos []gh.TeamRepo) reviewList {
	rl := reviewList{theme: th}
	for _, m := range members {
		rl.items = append(rl.items, reviewItem{kind: kindMember, label: m, included: true})
	}
	for _, r := range repos {
		rl.items = append(rl.items, reviewItem{
			kind:  kindRepo,
			label: r.Owner + "/" + r.Name,
			owner: r.Owner, name: r.Name,
			included: !r.Archived, // archived repos start excluded
		})
	}
	return rl
}

func (rl *reviewList) handleKey(k string) {
	switch k {
	case "up", "k":
		if rl.cursor > 0 {
			rl.cursor--
		}
	case "down", "j":
		if rl.cursor < len(rl.items)-1 {
			rl.cursor++
		}
	case "space", " ":
		if len(rl.items) > 0 {
			rl.items[rl.cursor].included = !rl.items[rl.cursor].included
		}
	}
}

func (rl *reviewList) toTeam(name, org, slug string) config.Team {
	t := config.Team{Name: name, Org: org, GHTeamSlug: slug}
	for _, it := range rl.items {
		if !it.included {
			continue
		}
		switch it.kind {
		case kindMember:
			t.Members = append(t.Members, config.Member{Login: it.label})
		case kindRepo:
			t.Repos = append(t.Repos, config.Repo{Owner: it.owner, Name: it.name})
		}
	}
	return t
}

// view renders the two sections with a simple scroll window of maxH rows.
func (rl *reviewList) view(maxH int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(rl.theme.Primary)
	var lines []string
	var cursorLine int

	section := kindMember
	lines = append(lines, title.Render("Members"))
	for i, it := range rl.items {
		if it.kind == kindRepo && section == kindMember {
			section = kindRepo
			lines = append(lines, "", title.Render("Repos"))
		}
		mark := "[ ]"
		if it.included {
			mark = "[x]"
		}
		line := fmt.Sprintf("  %s %s", mark, it.label)
		if i == rl.cursor {
			line = rl.theme.Selected.Render("▸ " + line[2:])
			cursorLine = len(lines)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", rl.theme.HelpDesc.Render("space toggle · ↑/↓ move · enter continue · esc back"))

	if maxH < 6 {
		maxH = 6
	}
	if len(lines) > maxH {
		start := cursorLine - maxH/2
		if start < 0 {
			start = 0
		}
		if start+maxH > len(lines) {
			start = len(lines) - maxH
		}
		lines = lines[start : start+maxH]
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
