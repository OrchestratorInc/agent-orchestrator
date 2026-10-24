// Share links for cloud sessions. The control plane mints a token per link and
// never stores it, so the link is the only copy. A shared link is a web page
// on the control plane, https://<cp>/app/share#<orgId>/<token>, that opens the
// desktop app through ao-app://share/<orgId>/<token>; the share rides in the
// fragment, which the browser never sends to the server. Pasting either form,
// or an older https://<host>/share/<orgId>/<token> link, opens the same share.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface ShareLinkTarget {
	orgId: string;
	token: string;
}

/** The link a person shares: a page on the control plane that opens the app. */
export function shareLinkUrl(controlPlaneUrl: string, orgId: string, token: string): string {
	return `${controlPlaneUrl.replace(/\/+$/, "")}/app/share#${encodeURIComponent(orgId)}/${encodeURIComponent(token)}`;
}

/** The share a pasted or opened link names, or undefined when it names none. */
export function parseShareLink(text: string): ShareLinkTarget | undefined {
	let url: URL;
	try {
		url = new URL(text.trim());
	} catch {
		return undefined;
	}
	let segments: string[];
	if (url.protocol === "ao-app:") {
		// ao-app://share/<org>/<token>: "share" parses as the host.
		if (url.hostname !== "share") return undefined;
		segments = url.pathname.split("/").filter(Boolean);
	} else if (url.protocol === "https:" || url.protocol === "http:") {
		const parts = url.pathname.split("/").filter(Boolean);
		if (parts.length === 2 && parts[0] === "app" && parts[1] === "share") {
			// https://<cp>/app/share#<org>/<token>
			segments = url.hash.replace(/^#/, "").split("/").filter(Boolean);
		} else if (parts[0] === "share") {
			segments = parts.slice(1);
		} else {
			return undefined;
		}
	} else {
		return undefined;
	}
	if (segments.length !== 2) return undefined;
	const [orgId, token] = segments.map((segment) => {
		try {
			return decodeURIComponent(segment);
		} catch {
			return "";
		}
	});
	if (!UUID.test(orgId) || !token) return undefined;
	return { orgId, token };
}
