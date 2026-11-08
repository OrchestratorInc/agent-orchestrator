/**
 * A repository's name however its remote is written (https or SSH, with or
 * without .git): host/namespace/name, lower-cased on GitHub, whose names
 * ignore case. Mirrors the daemon's domain.RepositoryKey. "" when url is not a
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
	const key = `${parsed.host.toLowerCase()}/${parts.join("/")}`;
	return github ? key.toLowerCase() : key;
}
