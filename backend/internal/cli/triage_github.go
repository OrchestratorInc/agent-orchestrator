package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
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
	stderr := &triageOutput{remaining: 4096}
	cmd := aoprocess.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_DEBUG=", "DEBUG=", "NO_COLOR=1", "CLICOLOR_FORCE=0")
	configureCLIBatch(cmd)
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = out, stderr, 100*time.Millisecond
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
		switch g.Auth {
		case "ok":
			filing = "available"
		case "failed":
			filing = "unavailable"
		}
	}
	return fmt.Sprintf("## GitHub\ngh=%s\nauth=%s\nlabel needs-triage=%s\nfiling=%s\n", gh, g.Auth, g.Label, filing)
}

var triageLevel = regexp.MustCompile(`(?i)(?:^|[\s{,])"?level"?\s*[:=]\s*"?([a-z]+)\b`)
var triagePlainLevel = regexp.MustCompile(`(?i)^(?:\d{4}-\d{2}-\d{2}\S*\s+)?\[?([a-z]+)\]?:?\s+`)
var triageMessage = regexp.MustCompile(`(?:^|[\s{,])"?(msg|message|error|err)(?:"\s*:|\s*=)\s*("(?:\\.|[^"\\])*"|[^\s,}]+)`)
var triageNoise = regexp.MustCompile(`\[[^\]]*\]|\S*[/\\]\S*|\b\S*\d\S*|\b\w+=\S+`)
var triageWord = regexp.MustCompile(`[a-z]{3,}`)

func triageLogMessages(logs string) []string {
	var messages []string
	seen := map[string]bool{}
	lines := strings.Split(logs, "\n")
	for i := len(lines) - 1; i >= 0 && len(messages) < 8; i-- {
		// Desktop marks daemon stderr as [error]; prefer the daemon's own level.
		level := triageLevel.FindStringSubmatchIndex(lines[i])
		if level == nil {
			level = triagePlainLevel.FindStringSubmatchIndex(lines[i])
		}
		if level == nil {
			continue
		}
		switch strings.ToUpper(lines[i][level[2]:level[3]]) {
		case "ERROR", "WARN", "WARNING", "PANIC", "FATAL":
		default:
			continue
		}
		message, detail := strings.TrimSpace(lines[i][level[1]:]), ""
		for _, field := range triageMessage.FindAllStringSubmatch(lines[i], -1) {
			value := field[2]
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
			if field[1] == "error" || field[1] == "err" {
				if value != "null" && value != "<nil>" {
					detail = value
				}
			} else {
				message = value
			}
		}
		if message != "" && !seen[message] {
			seen[message] = true
			if detail != "" {
				message += ": " + detail
			}
			messages = append(messages, strings.Join(strings.Fields(message), " "))
		}
	}
	return messages
}

func triageKeywords(logs string) []string {
	queries := []string{}
	seen := map[string]bool{}
	for _, message := range triageLogMessages(redactTriage(logs, "")) {
		words := []string{}
		for _, word := range triageWord.FindAllString(strings.ToLower(triageNoise.ReplaceAllString(message, " ")), -1) {
			switch word {
			case "the", "and", "for", "with", "from", "this", "that", "was", "were", "has", "have", "not", "msg", "level", "time", "error", "warn", "warning", "redacted", "secret":
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
		if len(words) >= 3 && !seen[query] {
			seen[query] = true
			queries = append(queries, query)
		}
		if len(queries) == 3 {
			break
		}
	}
	return queries
}

func triageDuplicates(ctx context.Context, path string, queries []string) string {
	queries = slices.DeleteFunc(slices.Clone(queries), func(query string) bool { return len(strings.Fields(query)) < 3 })
	if len(queries) == 0 {
		return "skipped: no error lines to search for\n"
	}
	if path == "" {
		return "unavailable: gh missing\n"
	}
	type candidate struct {
		Number            int
		Title, State, URL string
	}
	results := make([]string, len(queries))
	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Add(1)
		go func(i int, query string) {
			defer wg.Done()
			raw, err := triageGH(ctx, path, "issue", "list", "--repo", triageRepo, "--state", "all", "--search", query, "--limit", "5", "--json", "number,title,state,url")
			var rows []candidate
			if err != nil || json.Unmarshal([]byte(raw), &rows) != nil {
				reason := "search failed"
				if errors.Is(err, context.DeadlineExceeded) {
					reason = "timeout"
				}
				results[i] = "unavailable: issue " + query + ": " + reason + "\n"
				return
			}
			for _, row := range rows[:min(5, len(rows))] {
				results[i] += fmt.Sprintf("- issue #%d [%s] %s %s\n", row.Number, row.State, strings.Join(strings.Fields(row.Title), " "), row.URL)
			}
		}(i, query)
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
				if count == 5 {
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
