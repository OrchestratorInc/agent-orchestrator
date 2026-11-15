const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const CODE = /^[A-Za-z0-9]{22}$/;
const AFTER_CODE = /[^A-Za-z0-9]+$/;

export type ShareLinkTarget = { code: string } | { orgId: string; token: string };

export function isShareCode(value: unknown): value is string {
	return typeof value === "string" && CODE.test(value);
}

export function shareLinkUrl(controlPlaneUrl: string, orgId: string, token: string): string {
	return `${controlPlaneUrl.replace(/\/+$/, "")}/app/share#${encodeURIComponent(orgId)}/${encodeURIComponent(token)}`;
}

export function shareCodeLinkUrl(controlPlaneUrl: string, code: string): string {
	return `${controlPlaneUrl.replace(/\/+$/, "")}/s/${encodeURIComponent(code)}`;
}

export function createdShareLinkUrl(
	baseUrl: string,
	orgId: string,
	link: { code?: string; token?: string; shortUrl?: string },
): string | undefined {
	if (isShareCode(link.code)) {
		const named = link.shortUrl ? parseShareUrl(link.shortUrl) : undefined;
		if (named && "code" in named && named.code === link.code && link.shortUrl!.startsWith("https://")) return link.shortUrl;
		return shareCodeLinkUrl(baseUrl, link.code);
	}
	return link.token ? shareLinkUrl(baseUrl, orgId, link.token) : undefined;
}

export function parseShareLink(text: string): ShareLinkTarget | undefined {
	const trimmed = text.trim();
	const target = parseShareUrl(trimmed);
	if (target) return target;
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
		if (url.hostname !== "share") return undefined;
		segments = url.pathname.split("/").filter(Boolean);
		if (segments.length === 1) return isShareCode(segments[0]) ? { code: segments[0] } : undefined;
	} else if (url.protocol === "https:" || url.protocol === "http:") {
		const parts = url.pathname.split("/").filter(Boolean);
		if (parts.length === 2 && parts[0] === "s") {
			return isShareCode(parts[1]) ? { code: parts[1] } : undefined;
		}
		if (parts.length === 2 && parts[0] === "app" && parts[1] === "share") {
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
