package wizard

import (
	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/components"
)

// loadLists builds the two review steps from an import, or from the manual
// form dressed as one: every member starts included, every repo except the
// archived ones. Both lists are the checklist the app's pickers use, so
// they scroll, narrow on /, and take a and n. The import is not kept here;
// detailsMsg records it for the cut notice, and the manual form never
// imported anything.
func (m Model) loadLists(imp gh.TeamImport) Model {
	m.memberLogins = imp.Members
	checked := make([]bool, len(imp.Members))
	for i := range checked {
		checked[i] = true
	}
	m.members = components.NewChecklist(&m.theme, imp.Members, checked, "no members match")

	m.repoList = imp.Repos
	names := make([]string, len(imp.Repos))
	checked = make([]bool, len(imp.Repos))
	for i, r := range imp.Repos {
		names[i] = r.Owner + "/" + r.Name
		checked[i] = !r.Archived // archived repos start excluded
	}
	m.repos = components.NewChecklist(&m.theme, names, checked, "no repos match")

	m.members.SetHeight(m.checklistHeightFor(stepMembers))
	m.repos.SetHeight(m.checklistHeightFor(stepRepos))
	m.confirmEmptyRepos = false
	m.step = stepMembers
	return m
}

// toTeam is the profile the two lists describe: the checked members and
// repos in the order the API, or the form, gave them.
func (m Model) toTeam(name string) config.Team {
	t := config.Team{Name: name, Org: m.org, GHTeamSlug: m.slug}
	for i, login := range m.memberLogins {
		if m.members.Checked(i) {
			t.Members = append(t.Members, config.Member{Login: login})
		}
	}
	for i, r := range m.repoList {
		if m.repos.Checked(i) {
			t.Repos = append(t.Repos, config.Repo{Owner: r.Owner, Name: r.Name})
		}
	}
	return t
}
