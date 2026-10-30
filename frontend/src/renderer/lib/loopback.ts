export function isLoopbackHostname(value: string): boolean {
	const hostname = value.toLowerCase().replace(/^\[(.*)\]$/, "$1");
	return hostname === "localhost" || hostname.endsWith(".localhost") || hostname === "127.0.0.1" || hostname === "::1";
}

// A web link that names the machine it runs on: for a remote session, the
// remote host's, which the desktop can't reach under that name.
export function isHostLocalWebLink(href: string): boolean {
	try {
		// react-markdown percent-encodes IPv6 brackets in href/src attributes.
		const url = new URL(href.replace(/%5B|%5D/gi, (bracket) => decodeURIComponent(bracket)));
		if (url.protocol !== "http:" && url.protocol !== "https:") return false;
		const host = url.hostname.replace(/\.$/, "");
		return isLoopbackHostname(host) || /^127\.(?:\d{1,3}\.){2}\d{1,3}$/.test(host) || host === "0.0.0.0" || host === "[::]";
	} catch {
		return false;
	}
}
