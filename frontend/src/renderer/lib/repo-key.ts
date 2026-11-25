/**
 * A repository's name however its remote is written, as the daemon's
 * domain.RepositoryKey and AO Cloud's repoKey name it: https, http or SSH (also
 * git@host:path), with or without a trailing / or .git; the host lower-cased,
 * without a port or a leading "www.", GitHub's SSH-over-HTTPS host
 * ssh.github.com as github.com; then /namespace/name, the whole key
 * lower-cased on GitHub, whose names ignore case. "" when url is not a
 * repository URL.
 */
export function repoKey(url: string): string {
	let raw = url.trim();
	const scp = /^git@([^:/]+):(.+)$/.exec(raw);
	if (scp) raw = `ssh://git@${scp[1]}/${scp[2]}`;
	let parsed: URL;
	try {
		parsed = new URL(raw);
	} catch {
		return "";
	}
	if (!["https:", "http:", "ssh:"].includes(parsed.protocol) || !parsed.hostname || parsed.search || parsed.hash) return "";
	const parts = parsed.pathname.replace(/^\/+|\/+$/g, "").split("/");
	if (parts.length < 2) return "";
	parts[parts.length - 1] = parts[parts.length - 1].replace(/\.git$/, "");
	if (parts.some((part) => part === "" || part === "." || part === "..")) return "";
	const hostname = parsed.hostname.toLowerCase();
	const github = ["github.com", "www.github.com", "api.github.com"].includes(hostname) || hostname.endsWith(".github.com") || hostname.endsWith(".ghe.io");
	if (github && parts.length !== 2) return "";
	let host = hostname.replace(/^www\./, "");
	if (host === "ssh.github.com") host = "github.com";
	const key = `${host}/${parts.join("/")}`;
	return github ? key.toLowerCase() : key;
}
