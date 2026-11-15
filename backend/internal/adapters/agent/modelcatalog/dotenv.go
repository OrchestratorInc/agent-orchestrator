package modelcatalog

import "strings"

func envFileHasKey(path, name string) bool {
	raw, err := readModelConfig(path)
	if err != nil {
		return false
	}
	return credentialPresence(parseDotenv(raw)[name]) != ""
}

func parseDotenv(raw []byte) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "#") {
			value = ""
		} else if value != "" && (value[0] == '"' || value[0] == '\'') {
			end := 1
			for end < len(value) {
				if value[end] == value[0] {
					break
				}
				if value[end] == '\\' && end+1 < len(value) {
					end++
				}
				end++
			}
			if end == len(value) {
				continue
			}
			value = value[1:end]
		} else {
			for i := 1; i < len(value); i++ {
				if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
					value = strings.TrimSpace(value[:i])
					break
				}
			}
		}
		values[name] = value
	}
	return values
}
