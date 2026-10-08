import { lookup } from "node:dns/promises";
import { isIP } from "node:net";
import { isLocal } from "./render-check-proxy";

// The daemon routes that serve agent pages, and the inline-artifact origin.
const PAGE_ROUTE = /^\/api\/v1\/sessions\/[^/]+\/(renders\/[^/]+$|artifact-files\/)/;

/** Whether a frame URL is an agent page: a render or an HTML artifact framed in the chat. */
export function isAgentPageUrl(url: string): boolean {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		return false;
	}
	if (parsed.protocol !== "http:") return false;
	if (parsed.hostname.startsWith("ao-inline-artifact.") && parsed.hostname.endsWith(".localhost")) return true;
	return (parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost") && PAGE_ROUTE.test(parsed.pathname);
}

type Resolve = (host: string) => Promise<Array<{ address: string; family: number }>>;

const resolveAll: Resolve = (host) => lookup(host, { all: true, verbatim: true }).catch(() => []);

const DEFAULT_PORTS: Record<string, number> = { "http:": 80, "ws:": 80, "https:": 443, "wss:": 443 };

/**
 * Whether a request made from an agent page in the chat may go out. A page
 * may load public addresses and the daemon (its own route, other daemon
 * routes refuse its origin); nothing else on this computer or its network.
 * The same rule the render check enforces, here for the reader's frames.
 *
 * ponytail: the name is resolved here and again by Chromium, so a name that
 * changes its answer in between (DNS rebinding) can get past; the check
 * window's proxy connects only to the addresses it checked, if this ever needs
 * the same guarantee.
 */
export async function agentPageRequestAllowed(requestUrl: string, daemonPort: number | undefined, resolve: Resolve = resolveAll): Promise<boolean> {
	let url: URL;
	try {
		url = new URL(requestUrl);
	} catch {
		return false;
	}
	if (url.protocol === "data:" || url.protocol === "blob:" || url.protocol === "about:") return true;
	const defaultPort = DEFAULT_PORTS[url.protocol];
	if (defaultPort === undefined) return false;
	const port = url.port ? Number(url.port) : defaultPort;
	const host = url.hostname.replace(/^\[|\]$/g, "");
	const isDaemon = port === daemonPort;
	if (host === "localhost" || host.endsWith(".localhost")) return isDaemon;
	const family = isIP(host);
	const addresses = family ? [{ address: host, family }] : await resolve(host);
	const local = addresses.filter(({ address, family }) => isLocal(address, family));
	if (local.length === 0) return true;
	// Loopback on the daemon's own port is the daemon; any other local address is not.
	return isDaemon && local.every(({ address, family }) => (family === 6 ? address === "::1" : address.startsWith("127.")));
}
