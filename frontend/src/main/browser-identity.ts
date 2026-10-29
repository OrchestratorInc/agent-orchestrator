// Electron's default browser identity differs from Chrome's in ways bot
// managers (Cloudflare, Akamai, DataDome, reCAPTCHA) score as automation:
//   - the user agent carries the app's name/version and an `Electron/x` token,
//     and an unreduced Chrome version (Chrome sends `Chrome/<major>.0.0.0`);
//   - no `Sec-CH-UA*` request headers at all, while pages still see
//     `navigator.userAgentData`, an impossible pairing for a real Chrome;
//   - `Accept-Language` lists only the app locale while `navigator.languages`
//     lists every preferred language.
// This makes AO's Browser sessions present the Chromium build they really
// are, consistently across headers and page JavaScript. It does not claim to
// be Google Chrome: brands and versions come from the running Chromium.

type RequestHeaders = Record<string, string>;

// The slice of Electron's Session this module uses.
export type BrowserIdentitySession = {
	getUserAgent(): string;
	setUserAgent(userAgent: string, acceptLanguages?: string): void;
	webRequest: {
		onBeforeSendHeaders(
			listener: (
				details: { url: string; requestHeaders: RequestHeaders },
				callback: (response: { requestHeaders: RequestHeaders }) => void,
			) => void,
		): void;
	};
};

export type BrowserIdentity = {
	chromeVersion: string;
	platform: NodeJS.Platform;
	languages: string;
};

type Brand = { brand: string; version: string };

const configured = new WeakSet<object>();

export function browserUserAgent(electronUserAgent: string): string {
	return electronUserAgent
		.replace(/\(KHTML, like Gecko\) .*?Chrome\//, "(KHTML, like Gecko) Chrome/")
		.replace(/ Electron\/\S+/, "")
		.replace(/Chrome\/(\d+)[\d.]*/, "Chrome/$1.0.0.0");
}

// Chromium's GREASE brand list for a build without an embedder brand, seeded
// by the major version (GetGreasedUserAgentBrandVersion and
// GenerateBrandVersionList in components/embedder_support/user_agent_utils.cc).
export function chromiumBrands(major: number): Brand[] {
	const chars = [" ", "(", ":", "-", ".", "/", ")", ";", "=", "?", "_"];
	const versions = ["8", "99", "24"];
	const grease = {
		brand: `Not${chars[major % chars.length]}A${chars[(major + 1) % chars.length]}Brand`,
		version: versions[major % versions.length],
	};
	const brands: Brand[] = [];
	brands[major % 2] = grease;
	brands[(major + 1) % 2] = { brand: "Chromium", version: String(major) };
	return brands;
}

function platformHint(platform: NodeJS.Platform): string {
	if (platform === "darwin") return "macOS";
	if (platform === "win32") return "Windows";
	if (platform === "linux") return "Linux";
	return "Unknown";
}

export function clientHintHeaders(major: number, platform: NodeJS.Platform): Record<string, string> {
	return {
		"sec-ch-ua": chromiumBrands(major)
			.map(({ brand, version }) => `"${brand}";v="${version}"`)
			.join(", "),
		"sec-ch-ua-mobile": "?0",
		"sec-ch-ua-platform": `"${platformHint(platform)}"`,
	};
}

// Chrome sends low-entropy client hints to potentially trustworthy origins.
export function sendsClientHints(url: string): boolean {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		return false;
	}
	if (parsed.protocol === "https:") return true;
	if (parsed.protocol !== "http:") return false;
	const host = parsed.hostname;
	return host === "localhost" || host.endsWith(".localhost") || host === "[::1]" || /^127\./.test(host);
}

// Mirrors the list Electron gives pages as navigator.languages.
export function acceptLanguages(locale: string, preferred: readonly string[]): string {
	const languages = [...new Set([locale, ...preferred].filter(Boolean))];
	return languages.length > 0 ? languages.join(",") : "en-US";
}

// Must run before the session's first WebContents is created: Chromium reads
// the session user agent when a WebContents is constructed.
export function applyBrowserIdentity(session: BrowserIdentitySession, identity: BrowserIdentity): void {
	if (configured.has(session)) return;
	configured.add(session);
	session.setUserAgent(browserUserAgent(session.getUserAgent()), identity.languages);
	const hints = clientHintHeaders(Number.parseInt(identity.chromeVersion, 10), identity.platform);
	session.webRequest.onBeforeSendHeaders((details, callback) => {
		if (!sendsClientHints(details.url)) {
			callback({ requestHeaders: details.requestHeaders });
			return;
		}
		const present = new Set(Object.keys(details.requestHeaders).map((name) => name.toLowerCase()));
		const requestHeaders = { ...details.requestHeaders };
		for (const [name, value] of Object.entries(hints)) {
			if (!present.has(name)) requestHeaders[name] = value;
		}
		callback({ requestHeaders });
	});
}
