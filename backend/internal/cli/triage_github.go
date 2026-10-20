package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const triageRepo = "OrchestratorInc/agent-orchestrator"

type triageGitHub struct{ Path, Auth, Label string }

func triageGH(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out := &triageOutput{remaining: 32 << 10}
	cmd := aoprocess.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = out, out, 100*time.Millisecond
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.String(), err
}

func (c *commandContext) triagePreflight(ctx context.Context) triageGitHub {
	g := triageGitHub{Auth: "failed", Label: "unknown"}
	path, err := c.deps.LookPath("gh")
	if err != nil {
		return g
	}
	g.Path = path
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := triageGH(ctx, path, "auth", "status", "--hostname", "github.com")
		if errors.Is(err, context.DeadlineExceeded) {
			g.Auth = "unknown (timed out)"
		} else if err == nil {
			g.Auth = "ok"
		}
	}()
	go func() {
		defer wg.Done()
		raw, err := triageGH(ctx, path, "label", "list", "--repo", triageRepo, "--limit", "1000", "--json", "name")
		var labels []struct{ Name string }
		if errors.Is(err, context.DeadlineExceeded) {
			g.Label = "unavailable: timeout"
		}
		if err != nil || json.Unmarshal([]byte(raw), &labels) != nil {
			return
		}
		if len(labels) < 1000 {
			g.Label = "absent"
		}
		for _, label := range labels {
			if label.Name == "needs-triage" {
				g.Label = "present"
			}
		}
	}()
	wg.Wait()
	return g
}

func (g triageGitHub) summary() string {
	gh, filing := "missing", "unavailable"
	if g.Path != "" {
		gh = "present"
		filing = "unknown"
		if g.Auth == "ok" {
			filing = "available"
		} else if g.Auth == "failed" {
			filing = "unavailable"
		}
	}
	return fmt.Sprintf("## GitHub\ngh=%s\nauth=%s\nlabel needs-triage=%s\nfiling=%s\n", gh, g.Auth, g.Label, filing)
}

var triageLevel = regexp.MustCompile(`(?i)\b(?:ERROR|WARN|WARNING)\b`)
var triageMessage = regexp.MustCompile(`(?:msg|message)"?\s*[:=]\s*"([^"]*)"`)
var triageNoise = regexp.MustCompile(`\[[^\]]*\]|\S*[/\\]\S*|\b\S*[0-9]\S*|\b\w+=\S+`)
var triageWord = regexp.MustCompile(`[a-z]{3,}`)

func triageLogMessages(logs string) []string {
	var messages []string
	seen := map[string]bool{}
	lines := strings.Split(logs, "\n")
	for i := len(lines) - 1; i >= 0 && len(messages) < 8; i-- {
		loc := triageLevel.FindStringIndex(lines[i])
		if loc == nil {
			continue
		}
		message := strings.TrimSpace(lines[i][loc[1]:])
		if m := triageMessage.FindStringSubmatch(lines[i]); m != nil {
			message = m[1]
		}
		if message != "" && !seen[message] {
			seen[message] = true
			messages = append(messages, message)
		}
	}
	return messages
}

func triageKeywords(logs string, state daemonState) []string {
	queries := []string{}
	seen := map[string]bool{}
	limit := 3
	if state != stateReady {
		limit = 2
	}
	for _, message := range triageLogMessages(logs) {
		words := []string{}
		for _, word := range triageWord.FindAllString(strings.ToLower(triageNoise.ReplaceAllString(message, " ")), -1) {
			if strings.Contains(" the and for with from this that was were has have not msg level time error warn warning redacted secret ", " "+word+" ") {
				continue
			}
			if !slices.Contains(words, word) {
				words = append(words, word)
			}
			if len(words) == 4 {
				break
			}
		}
		query := strings.Join(words, " ")
		if len(words) >= 2 && !seen[query] {
			seen[query] = true
			queries = append(queries, query)
		}
		if len(queries) == limit {
			break
		}
	}
	if state != stateReady {
		if state == "" {
			state = "unavailable"
		}
		queries = append(queries, "daemon "+string(state))
	}
	return queries
}

func triageDuplicates(ctx context.Context, path string, queries []string) string {
	if path == "" {
		return "unavailable: gh missing\n"
	}
	if len(queries) == 0 {
		return "unavailable: no search keywords\n"
	}
	type candidate struct {
		Number            int
		Title, State, URL string
	}
	results := make([]string, len(queries)*2)
	var wg sync.WaitGroup
	for i, query := range queries {
		for j, kind := range []string{"issue", "pr"} {
			wg.Add(1)
			go func(i int, query, kind string) {
				defer wg.Done()
				raw, err := triageGH(ctx, path, kind, "list", "--repo", triageRepo, "--state", "all", "--search", query, "--limit", "5", "--json", "number,title,state,url")
				var rows []candidate
				if err != nil || json.Unmarshal([]byte(raw), &rows) != nil {
					reason := "search failed"
					if errors.Is(err, context.DeadlineExceeded) {
						reason = "timeout"
					}
					results[i] = "unavailable: " + kind + " " + query + ": " + reason + "\n"
					return
				}
				for _, row := range rows[:min(5, len(rows))] {
					results[i] += fmt.Sprintf("- %s #%d [%s] %s %s\n", kind, row.Number, row.State, strings.Join(strings.Fields(row.Title), " "), row.URL)
				}
			}(i*2+j, query, kind)
		}
	}
	wg.Wait()
	var b strings.Builder
	seen, count := map[string]bool{}, 0
	for _, result := range results {
		for _, line := range strings.Split(strings.TrimSpace(result), "\n") {
			if line == "" {
				continue
			}
			key := line
			if strings.HasPrefix(line, "- ") {
				key = line[strings.LastIndex(line, " ")+1:]
				if count == 10 {
					continue
				}
			}
			if !seen[key] {
				seen[key] = true
				b.WriteString(line + "\n")
				if strings.HasPrefix(line, "- ") {
					count++
				}
			}
		}
	}
	if b.Len() == 0 {
		fmt.Fprintf(&b, "none found for: %s\n", strings.Join(queries, "; "))
	}
	return b.String()
}
