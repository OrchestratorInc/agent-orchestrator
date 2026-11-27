import type { Session } from "electron";

/**
 * The packaged app's renderer origin. A Cloud link's file host lets only the
 * link's viewer page and this origin frame its pages (frame-ancestors).
 */
const PACKAGED_RENDERER_ORIGIN = "app://renderer";

/**
 * Returns a Content-Security-Policy that also lets devOrigin frame the page,
 * when the policy already lets the packaged app frame it; otherwise the policy
 * unchanged. Only the frame-ancestors directive changes.
 */
export function allowDevRendererToFrame(policy: string, devOrigin: string): string {
	return policy
		.split(";")
		.map((directive) => {
			const parts = directive.trim().split(/\s+/);
			if (parts[0]?.toLowerCase() !== "frame-ancestors") return directive;
			if (!parts.includes(PACKAGED_RENDERER_ORIGIN) || parts.includes(devOrigin)) return directive;
			return `${directive.trimEnd()} ${devOrigin}`;
		})
		.join(";");
}

/**
 * A dev build's renderer is served by Vite at http://localhost:<port>, not at
 * app://renderer, so a Cloud link's file host refuses to be framed by it
 * (ERR_BLOCKED_BY_RESPONSE): a published artifact shows as a blank page and
 * its comments cannot point at its parts. For dev builds only, this lets the
 * dev renderer frame whatever the packaged app may frame, in the app's own
 * session. Packaged builds never call it.
 */
export function installDevRendererFraming(target: Session, devServerUrl: string): void {
	const devOrigin = new URL(devServerUrl).origin;
	target.webRequest.onHeadersReceived({ urls: ["https://*/*"] }, (details, callback) => {
		const headers = details.responseHeaders;
		if (details.resourceType !== "subFrame" || !headers) {
			callback({});
			return;
		}
		let changed = false;
		const next: Record<string, string[]> = {};
		for (const [name, values] of Object.entries(headers)) {
			if (name.toLowerCase() !== "content-security-policy") {
				next[name] = values;
				continue;
			}
			next[name] = values.map((value) => {
				const updated = allowDevRendererToFrame(value, devOrigin);
				if (updated !== value) changed = true;
				return updated;
			});
		}
		callback(changed ? { responseHeaders: next } : {});
	});
}
