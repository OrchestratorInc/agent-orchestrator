// Share links for cloud sessions. The control plane mints a short code per
// link and keeps only its hash, so the link is the only copy. A shared link is
// a web page on the control plane, https://<cp>/s/<code>, that opens the
// desktop app through ao-app://share/<code>. A control plane without codes
// shares https://<cp>/app/share#<orgId>/<token> instead, which opens
// ao-app://share/<orgId>/<token>; that share rides in the fragment, which the
// browser never sends to the server. Pasting any of these, or an older
// https://<host>/share/<orgId>/<token> link, opens the same share.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
// A share code is exactly 22 letters or digits; nothing else is one.
const CODE = /^[A-Za-z0-9]{22}$/;
// What a chat app can leave after a pasted link: punctuation, quotes, a
// trailing slash, invisible characters. A code never contains any of them.
const AFTER_CODE = /[^A-Za-z0-9]+$/;

/** The share a link names: its short code, or the organization and token of a link from before codes. */
export type ShareLinkTarget = { code: string } | { orgId: string; token: string };

/** Whether a value is a share code the control plane mints. */
export function isShareCode(value: unknown): value is string {
	return typeof value === "string" && CODE.test(value);
}

/** The link a person shares: a page on the control plane that opens the app. */
export function shareLinkUrl(controlPlaneUrl: string, orgId: string, token: string): string {
	return `${controlPlaneUrl.replace(/\/+$/, "")}/app/share#${encodeURIComponent(orgId)}/${encodeURIComponent(token)}`;
}

/** The short link a person shares for a link with a code. */
export function shareCodeLinkUrl(controlPlaneUrl: string, code: string): string {
	return `${controlPlaneUrl.replace(/\/+$/, "")}/s/${encodeURIComponent(code)}`;
}

/** The share a pasted or opened link names, or undefined when it names none. */
export function parseShareLink(text: string): ShareLinkTarget | undefined {
	const trimmed = text.trim();
	const target = parseShareUrl(trimmed);
	if (target) return target;
	// A short link with punctuation after it ("…/s/<code>).") still opens. A
	// token link is only ever read as pasted.
	const stripped = parseShareUrl(trimmed.replace(AFTER_CODE, ""));
	return stripped && "code" in stripped ? stripped : undefined;
}

function parseShareUrl(text: string): ShareLinkTarget | undefined {
	let url: URL;
	try {
		url = new URL(text);
	} catch {
		return undefined;
	}
	let segments: string[];
	if (url.protocol === "ao-app:") {
		// ao-app://share/<code> or ao-app://share/<org>/<token>: "share" parses as the host.
		if (url.hostname !== "share") return undefined;
		segments = url.pathname.split("/").filter(Boolean);
		if (segments.length === 1) return isShareCode(segments[0]) ? { code: segments[0] } : undefined;
	} else if (url.protocol === "https:" || url.protocol === "http:") {
		const parts = url.pathname.split("/").filter(Boolean);
		if (parts.length === 2 && parts[0] === "s") {
			// https://<cp>/s/<code>
			return isShareCode(parts[1]) ? { code: parts[1] } : undefined;
		}
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
