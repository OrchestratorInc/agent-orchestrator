import { lookup } from "node:dns/promises";
import { isIP } from "node:net";
import { isOwnPageRequest } from "../shared/agent-page-url";
import { isLocal } from "./render-check-proxy";

type Resolve = (host: string) => Promise<Array<{ address: string; family: number }>>;

const resolveAll: Resolve = (host) => lookup(host, { all: true, verbatim: true }).catch(() => []);

const SCHEMES = new Set(["http:", "https:", "ws:", "wss:"]);

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
	return isOwnPageRequest(page, url);
}
