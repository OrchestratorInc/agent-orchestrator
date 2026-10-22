// Share links for cloud sessions. The control plane mints a token per link and
// never stores it, so the link is the only copy. The desktop opens
// ao-app://share/<orgId>/<token>; a pasted web link of the same shape
// (https://<host>/share/<orgId>/<token>, as the control plane builds it) opens
// the same share.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface ShareLinkTarget {
	orgId: string;
	token: string;
}

/** The link the desktop opens for a share. */
export function shareLinkUrl(orgId: string, token: string): string {
	return `ao-app://share/${encodeURIComponent(orgId)}/${encodeURIComponent(token)}`;
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
		if (parts[0] !== "share") return undefined;
		segments = parts.slice(1);
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
