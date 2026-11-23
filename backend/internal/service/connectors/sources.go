package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	maxSourceBytes = 64 << 20
	agentClaude    = "Claude Code"
	agentCodex     = "Codex"
	agentCursor    = "Cursor"
)

// ImportProject is a project whose folder can hold tool-server definitions.
type ImportProject struct {
	ID   string
	Path string
}

// ImportCandidate is a tool server found in the configuration of another agent. It carries no stored value.
type ImportCandidate struct {
	ID         string
	Agent      string
	Source     string
	Key        string
	Transport  Transport
	URL        string
	Command    string
	Args       []string
	EnvKeys    []string
	HeaderKeys []string
	NeedsValue []string
	ProjectID  string
	Exists     bool
}

var sourceVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

type sourceDef struct {
	Type      string            `json:"type" toml:"type"`
	Command   string            `json:"command" toml:"command"`
	Args      []string          `json:"args" toml:"args"`
	Env       map[string]string `json:"env" toml:"env"`
	URL       string            `json:"url" toml:"url"`
	Headers   map[string]string `json:"headers" toml:"http_headers"`
	BearerEnv string            `json:"-" toml:"bearer_token_env_var"`
}

type sourceServer struct {
	agent, path, name, projectID string
	def                          sourceDef
}

type jsonSource struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Projects   map[string]struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	} `json:"projects"`
}

// SetImportProjects sets how import learns the projects of this computer.
func (s *Service) SetImportProjects(projects func(ctx context.Context) []ImportProject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.importProjects = projects
}

// NativeNames returns the names of the tool servers that an agent reads from its own configuration.
func NativeNames(harness domain.AgentHarness, workspace string, env map[string]string) map[string]bool {
	names := map[string]bool{}
	home, _ := os.UserHomeDir()
	switch harness {
	case domain.HarnessClaudeCode:
		dir := home
		if env["CLAUDE_CONFIG_DIR"] != "" {
			dir = env["CLAUDE_CONFIG_DIR"]
		}
		file := readJSONSource(filepath.Join(dir, ".claude.json"))
		for name := range file.MCPServers {
			names[name] = true
		}
		for name := range file.Projects[workspace].MCPServers {
			names[name] = true
		}
		if workspace != "" {
			for name := range readJSONSource(filepath.Join(workspace, ".mcp.json")).MCPServers {
				names[name] = true
			}
		}
	case domain.HarnessCodex:
		dir := filepath.Join(home, ".codex")
		if env["CODEX_HOME"] != "" {
			dir = env["CODEX_HOME"]
		}
		for name := range readCodexSource(filepath.Join(dir, "config.toml")) {
			names[name] = true
		}
	}
	for name := range maps.Clone(names) {
		names[importKey(name)] = true
	}
	return names
}

// ImportCandidates lists the tool servers found in the configuration of Claude Code, Codex and Cursor.
func (s *Service) ImportCandidates(ctx context.Context) ([]ImportCandidate, error) {
	found := s.foundServers(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	out := make([]ImportCandidate, 0, len(found))
	seen := map[string]bool{}
	for _, server := range found {
		in, needs, ok := server.input(nil)
		id := server.id()
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ImportCandidate{
			ID: id, Agent: server.agent, Source: server.path, Key: in.Key, Transport: in.Transport,
			URL: MaskedURL(in.URL), Command: in.Command, Args: slices.Clone(in.Args),
			EnvKeys: slices.Sorted(maps.Keys(in.Env)), HeaderKeys: slices.Sorted(maps.Keys(in.Headers)),
			NeedsValue: needs, ProjectID: server.projectID, Exists: s.index(in.Key) >= 0,
		})
	}
	slices.SortStableFunc(out, func(a, b ImportCandidate) int {
		return strings.Compare(a.Agent+"\x00"+a.Key+"\x00"+a.Source, b.Agent+"\x00"+b.Key+"\x00"+b.Source)
	})
	return out, nil
}

// Import adds the chosen candidates as connectors. values holds what the user typed, by candidate ID and name.
func (s *Service) Import(ctx context.Context, ids []string, values map[string]map[string]string) ([]Connector, error) {
	found := map[string]sourceServer{}
	for _, server := range s.foundServers(ctx) {
		if _, taken := found[server.id()]; !taken {
			found[server.id()] = server
		}
	}
	inputs := make([]Input, 0, len(ids))
	keys := map[string]bool{}
	for _, id := range ids {
		server, ok := found[id]
		if !ok {
			return nil, apierr.NotFound("CONNECTOR_IMPORT_NOT_FOUND", "That server is not in the configuration now")
		}
		in, needs, ok := server.input(values[id])
		if !ok {
			return nil, invalid("CONNECTOR_IMPORT_UNSUPPORTED", server.name+" uses a transport that connectors do not support")
		}
		if len(needs) > 0 {
			return nil, invalid("CONNECTOR_IMPORT_VALUE", server.name+" needs a value for "+strings.Join(needs, ", "))
		}
		rec, err := normalize(in, record{})
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		taken := s.load() == nil && s.keyTaken(rec.Key, "")
		s.mu.Unlock()
		if taken || keys[rec.Key] {
			return nil, errKeyExists(rec.Key)
		}
		keys[rec.Key] = true
		inputs = append(inputs, in)
	}
	out := make([]Connector, 0, len(inputs))
	for _, in := range inputs {
		created, err := s.Create(ctx, in)
		if err != nil {
			return out, err
		}
		out = append(out, created)
	}
	return out, nil
}

func (s *Service) foundServers(ctx context.Context) []sourceServer {
	s.mu.Lock()
	lookup := s.importProjects
	s.mu.Unlock()
	var projects []ImportProject
	if lookup != nil {
		projects = lookup(ctx)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []sourceServer
	add := func(agent, path, projectID string, defs map[string]sourceDef) {
		for _, name := range slices.Sorted(maps.Keys(defs)) {
			out = append(out, sourceServer{agent: agent, path: path, name: name, projectID: projectID, def: defs[name]})
		}
	}
	claudePath := filepath.Join(home, ".claude.json")
	claude := readJSONSource(claudePath)
	add(agentClaude, claudePath, "", decodeDefs(claude.MCPServers))
	add(agentCodex, filepath.Join(home, ".codex", "config.toml"), "", readCodexSource(filepath.Join(home, ".codex", "config.toml")))
	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	add(agentCursor, cursorPath, "", decodeDefs(readJSONSource(cursorPath).MCPServers))
	for _, project := range projects {
		if project.Path == "" {
			continue
		}
		add(agentClaude, claudePath, project.ID, decodeDefs(claude.Projects[project.Path].MCPServers))
		for agent, rel := range map[string]string{agentClaude: ".mcp.json", agentCursor: filepath.Join(".cursor", "mcp.json")} {
			path := filepath.Join(project.Path, rel)
			add(agent, path, project.ID, decodeDefs(readJSONSource(path).MCPServers))
		}
	}
	return out
}

func importKey(name string) string {
	key := strings.Trim(slugRun.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(key) > 32 {
		key = strings.Trim(key[:32], "-")
	}
	return key
}

func (f sourceServer) id() string {
	sum := sha256.Sum256([]byte(f.agent + "\x00" + f.path + "\x00" + f.projectID + "\x00" + f.name))
	return hex.EncodeToString(sum[:8])
}

func (f sourceServer) input(typed map[string]string) (in Input, needs []string, ok bool) {
	key := importKey(f.name)
	if key == "" || reservedKeys[key] {
		return Input{}, nil, false
	}
	in = Input{Key: key, Name: f.name, ProjectID: f.projectID}
	fill := func(source map[string]string) map[string]string {
		out := make(map[string]string, len(source))
		for name, value := range source {
			if strings.Contains(value, "${") || value == "" {
				value = typed[name]
			}
			if value == "" {
				needs = append(needs, name)
			}
			out[name] = value
		}
		return out
	}
	expand := func(text string) string {
		return sourceVariable.ReplaceAllStringFunc(text, func(match string) string {
			parts := sourceVariable.FindStringSubmatch(match)
			if typed[parts[1]] != "" {
				return typed[parts[1]]
			}
			if strings.Contains(match, ":-") {
				return parts[2]
			}
			needs = append(needs, parts[1])
			return match
		})
	}
	kind := strings.ToLower(strings.TrimSpace(f.def.Type))
	switch {
	case f.def.URL != "" && (kind == "" || kind == "http" || kind == "streamable-http" || kind == "streamable_http"):
		in.Transport, in.URL, in.Auth = TransportHTTP, expand(f.def.URL), AuthOAuth
		headers := maps.Clone(f.def.Headers)
		if f.def.BearerEnv != "" {
			if headers == nil {
				headers = map[string]string{}
			}
			headers["Authorization"] = ""
			if token := os.Getenv(f.def.BearerEnv); token != "" {
				headers["Authorization"] = "Bearer " + token
			}
		}
		if in.Headers = fill(headers); len(in.Headers) > 0 {
			in.Auth = AuthHeader
		}
	case f.def.Command != "" && (kind == "" || kind == "stdio"):
		in.Transport, in.Command, in.Env = TransportStdio, expand(f.def.Command), fill(f.def.Env)
		for _, arg := range f.def.Args {
			in.Args = append(in.Args, expand(arg))
		}
	default:
		return Input{}, nil, false
	}
	slices.Sort(needs)
	return in, slices.Compact(needs), true
}

func readSource(path string) []byte {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxSourceBytes {
		return nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is an agent's own configuration file under the user's home or a project folder.
	if err != nil {
		return nil
	}
	return data
}

func readJSONSource(path string) jsonSource {
	var out jsonSource
	_ = json.Unmarshal(readSource(path), &out)
	return out
}

func readCodexSource(path string) map[string]sourceDef {
	var out struct {
		MCPServers map[string]sourceDef `toml:"mcp_servers"`
	}
	_ = toml.Unmarshal(readSource(path), &out)
	return out.MCPServers
}

func decodeDefs(raw map[string]json.RawMessage) map[string]sourceDef {
	out := make(map[string]sourceDef, len(raw))
	for name, value := range raw {
		var def sourceDef
		if json.Unmarshal(value, &def) == nil {
			out[name] = def
		}
	}
	return out
}
