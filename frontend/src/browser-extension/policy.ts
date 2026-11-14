export type SitePolicy = {
	deny: string[];
	allow: string[];
	syntheticInput: string[];
	trustedInput: boolean;
};

export const DEFAULT_SITE_POLICY: SitePolicy = {
	deny: [
		"accounts.google.com",
		"myaccount.google.com",
		"passwords.google.com",
		"pay.google.com",
		"paypal.com",
		"stripe.com",
		"1password.com",
		"lastpass.com",
		"bitwarden.com",
		"dashlane.com",
		"chromewebstore.google.com",
	],
	allow: [],
	syntheticInput: [],
	trustedInput: true,
};

export function hostMatches(host: string, pattern: string): boolean {
	const normalized = pattern.trim().toLowerCase().replace(/^\*\./, "").replace(/\.$/, "");
	if (!normalized) return false;
	if (normalized === "*") return true;
	const target = host.toLowerCase().replace(/\.$/, "");
	return target === normalized || target.endsWith(`.${normalized}`);
}

export type PolicyDecision = { allowed: true } | { allowed: false; reason: string };

export function evaluateUrl(policy: SitePolicy, rawUrl: string | undefined): PolicyDecision {
	if (!rawUrl || rawUrl === "about:blank") return { allowed: true };
	let url: URL;
	try {
		url = new URL(rawUrl);
	} catch {
		return { allowed: false, reason: "The page address is not a web URL" };
	}
	if (url.protocol !== "http:" && url.protocol !== "https:") {
		return { allowed: false, reason: `Agents from Orchestrator cannot act on ${url.protocol} pages` };
	}
	const host = url.hostname;
	if (policy.deny.some((pattern) => hostMatches(host, pattern))) {
		return { allowed: false, reason: `${host} is on the Orchestrator Browser Bridge deny list` };
	}
	if (policy.allow.length > 0 && !policy.allow.some((pattern) => hostMatches(host, pattern))) {
		return { allowed: false, reason: `${host} is not on the Orchestrator Browser Bridge allow list` };
	}
	return { allowed: true };
}

export function trustedInputAllowed(policy: SitePolicy, rawUrl: string | undefined): boolean {
	if (!policy.trustedInput) return false;
	try {
		const host = new URL(rawUrl ?? "").hostname;
		return !policy.syntheticInput.some((pattern) => hostMatches(host, pattern));
	} catch {
		return policy.trustedInput;
	}
}

export function parsePolicy(value: unknown): SitePolicy {
	if (!value || typeof value !== "object") return { ...DEFAULT_SITE_POLICY };
	const raw = value as Record<string, unknown>;
	const list = (input: unknown, fallback: string[]) =>
		Array.isArray(input) ? input.filter((item): item is string => typeof item === "string" && item.trim() !== "") : fallback;
	return {
		deny: list(raw.deny, DEFAULT_SITE_POLICY.deny),
		allow: list(raw.allow, []),
		syntheticInput: list(raw.syntheticInput, []),
		trustedInput: typeof raw.trustedInput === "boolean" ? raw.trustedInput : true,
	};
}

export function parseHostList(text: string): string[] {
	return text
		.split(/[\n,]/)
		.map((item) => item.trim().toLowerCase())
		.filter(Boolean);
}
