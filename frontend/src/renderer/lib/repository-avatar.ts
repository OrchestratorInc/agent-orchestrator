export function repositoryOwnerAvatar(repo: string): { owner: string; url: string } | null {
	const value = repo.trim();
	const scp = value.match(/^[^/@:\s]+@([^/:\s]+):(.+)$/);
	let host: string;
	let pathname: string;
	if (scp?.[1] && scp[2]) {
		host = scp[1].toLowerCase();
		pathname = scp[2];
	} else {
		try {
			const parsed = new URL(value.includes("://") ? value : `https://${value}`);
			host = parsed.hostname.toLowerCase();
			pathname = parsed.pathname;
		} catch {
			return null;
		}
	}
	const owner = pathname.replace(/^\/+|\/+$/g, "").split("/")[0]?.replace(/\.git$/, "");
	if (!owner) return null;
	const encodedOwner = encodeURIComponent(owner);
	if (host === "github.com") return { owner, url: `https://github.com/${encodedOwner}.png?size=64` };
	if (host === "gitlab.com") return { owner, url: `https://gitlab.com/-/avatar?username=${encodedOwner}` };
	if (host === "bitbucket.org") return { owner, url: `https://bitbucket.org/account/${encodedOwner}/avatar/64/` };
	return { owner, url: `https://unavatar.io/${encodeURIComponent(host)}/${encodedOwner}` };
}
