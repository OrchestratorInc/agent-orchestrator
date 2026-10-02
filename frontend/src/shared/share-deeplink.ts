// Parser for read-only session share deep links minted by the AO Cloud control
// plane: ao-app://share/<orgId>/<linkId>#<secret>. Everything here is
// validation only — a parsed invite is handed to the renderer to show a consent
// dialog and is never redeemed automatically.

export type ShareInvite = {
	orgId: string;
	linkId: string;
	secret: string;
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
// The CP mints 32 random bytes as unpadded base64url (43 chars).
const SECRET = /^[A-Za-z0-9_-]{43}$/;

/** Returns the invite for a well-formed share link, or null for anything else. */
export function parseShareDeepLink(raw: string): ShareInvite | null {
	if (raw.length > 512) return null;
	let url: URL;
	try {
		url = new URL(raw);
	} catch {
		return null;
	}
	if (url.protocol !== "ao-app:" || url.hostname !== "share") return null;
	if (url.search !== "" || url.username !== "" || url.password !== "" || url.port !== "") return null;
	const segments = url.pathname.split("/").filter((segment) => segment !== "");
	if (segments.length !== 2) return null;
	const [orgId, linkId] = segments;
	const secret = url.hash.replace(/^#/, "");
	if (!UUID.test(orgId) || !UUID.test(linkId) || !SECRET.test(secret)) return null;
	return { orgId: orgId.toLowerCase(), linkId: linkId.toLowerCase(), secret };
}
