package gitlab

import "strings"

func NormalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func IsGitLabDotCom(host string) bool {
	host = NormalizeHost(host)
	return host == "" || host == "gitlab.com" || host == "www.gitlab.com"
}
