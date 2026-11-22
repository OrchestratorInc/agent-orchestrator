package providerkeys

import "sort"

// ToolKeyGMI is the provider variable for GMI Cloud's media generation API.
// AO's `ao mcp` server offers the GMI tools to a session when this key is
// saved.
const ToolKeyGMI = "GMI_API_KEY"

// toolKeys are provider variables AO's own tools read, rather than a harness
// reads. A session never receives one in its environment: the tool reads the
// key from AO's store instead, so nothing AO launches can read the credential
// out of an environment variable, and the credential never reaches a process
// AO started.
var toolKeys = map[string]string{
	ToolKeyGMI: "GMI Cloud",
}

// IsToolKey reports whether name is a key AO's own tools read.
func IsToolKey(name string) bool {
	_, ok := toolKeys[name]
	return ok
}

// ToolKeyLabel returns the display name of a tool key, or "" when name is not
// one.
func ToolKeyLabel(name string) string { return toolKeys[name] }

// ToolKeys lists the names of every tool key, sorted.
func ToolKeys() []string {
	names := make([]string, 0, len(toolKeys))
	for name := range toolKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
