import { lookup } from "node:dns/promises";
import { isIP } from "node:net";
import { isOwnPageRequest } from "../shared/agent-page-url";
import { isLocal } from "./render-check-proxy";

type Resolve = (host: string) => Promise<Array<{ address: string; family: number }>>;

const resolveAll: Resolve = (host) => lookup(host, { all: true, verbatim: true }).catch(() => []);

const SCHEMES = new Set(["http:", "https:", "ws:", "wss:"]);

/**
 * Whether a request made from agent page `pageUrl` in the chat may go out. A
 * page may load public addresses and its own files on this computer; nothing
 * else here or on its network: not the daemon's API, not another session's
 * origin (a preview origin the daemon trusts, say), not another port.
 * The same rule the render check enforces, here for the reader's frames.
 *
 * ponytail: the name is resolved here and again by Chromium, so a name that
 * changes its answer in between (DNS rebinding) can get past; the check
 * window's proxy connects only to the addresses it checked, if this ever needs
 * the same guarantee.
 */
export async function agentPageRequestAllowed(requestUrl: string, pageUrl: string, resolve: Resolve = resolveAll): Promise<boolean> {
	let url: URL;
	let page: URL;
	try {
		url = new URL(requestUrl);
		page = new URL(pageUrl);
	} catch {
		return false;
	}
	if (url.protocol === "data:" || url.protocol === "blob:" || url.protocol === "about:") return true;
	if (!SCHEMES.has(url.protocol)) return false;
	const host = url.hostname.replace(/^\[|\]$/g, "");
	if (host === "localhost" || host.endsWith(".localhost")) return isOwnPageRequest(page, url);
	const family = isIP(host);
	const addresses = family ? [{ address: host, family }] : await resolve(host);
	if (!addresses.some(({ address, family }) => isLocal(address, family))) return true;
	// Only the page's own route on the daemon, named as loopback; any other local address is refused.
	return isOwnPageRequest(page, url);
}
