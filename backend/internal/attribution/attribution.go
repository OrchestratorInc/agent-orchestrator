// Package attribution puts Orchestrator's name on the commits and pull
// requests its sessions make.
//
// Agents commit and open PRs with the person's own git and gh login, so the
// person stays the author. Orchestrator adds a Co-authored-by trailer to each
// commit (through a git hook installed per session worktree) and one footer
// line to each PR body (through the worker prompt). GitHub shows the bot's
// avatar next to the person's on every commit that carries the trailer.
//
// The bot identity lives in Bot, and only there, so moving from the test app to
// the production GitHub App is a one-line change.
package attribution

import "fmt"

// Identity is the GitHub App bot that is credited as co-author.
type Identity struct {
	// Name is the display name written in the trailer.
	Name string
	// Slug is the GitHub App slug; the bot's login is "<slug>[bot]".
	Slug string
	// UserID is the bot account's numeric GitHub user id
	// (gh api users/<slug>%5Bbot%5D --jq .id). GitHub links the trailer to the
	// bot through the noreply email built from it.
	UserID int64
}

// Bot is the identity Orchestrator credits.
//
// This is the test app on somewherelostt. Swap it for the production App
// (slug and user id) once that is registered.
var Bot = Identity{Name: "Orchestrator", Slug: "orchestrator-dev-somewherelost", UserID: 353898192}

// Login is the bot's GitHub login, for example "orchestrator[bot]".
func (i Identity) Login() string { return i.Slug + "[bot]" }

// Email is the bot's GitHub noreply address. GitHub resolves it to the bot
// account and shows the bot's avatar on commits that list it.
func (i Identity) Email() string {
	return fmt.Sprintf("%d+%s@users.noreply.github.com", i.UserID, i.Login())
}

// CoAuthor is the "Name <email>" value of the Co-authored-by trailer.
func (i Identity) CoAuthor() string { return fmt.Sprintf("%s <%s>", i.Name, i.Email()) }

// Trailer is the full trailer line added to commit messages.
func (i Identity) Trailer() string { return "Co-authored-by: " + i.CoAuthor() }

// PRFooter is the line appended to the end of every PR body.
const PRFooter = "Opened with [Orchestrator](https://orchestrator.inc)"
