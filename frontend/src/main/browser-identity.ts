type RequestHeaders = Record<string, string>;

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
		.replace(/Chrome\/(\d+)[\d.]*/, "Chrome/$1.0.0.0");
}

export function applyUserAgentFallback(target: { userAgentFallback: string }): void {
	target.userAgentFallback = browserUserAgent(target.userAgentFallback);
}

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

export function acceptLanguages(locale: string, preferred: readonly string[]): string {
	const languages = [...new Set([locale, ...preferred].filter(Boolean))];
	return languages.length > 0 ? languages.join(",") : "en-US";
}

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
