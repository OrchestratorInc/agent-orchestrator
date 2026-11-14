import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	acceptLanguages,
	applyBrowserIdentity,
	applyUserAgentFallback,
	browserUserAgent,
	chromiumBrands,
	clientHintHeaders,
	sendsClientHints,
} from "./browser-identity";

const ELECTRON_MAC_UA =
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Orchestrator.inc/0.14.3-nightly.202610161732 Chrome/130.0.6723.191 Electron/33.4.11 Safari/537.36";

type HeadersListener = (
	details: { url: string; requestHeaders: Record<string, string> },
	callback: (response: { requestHeaders: Record<string, string> }) => void,
) => void;

function fakeSession(userAgent = ELECTRON_MAC_UA) {
	const listeners: HeadersListener[] = [];
	const calls: Array<[string, string | undefined]> = [];
	return {
		calls,
		listeners,
		session: {
			getUserAgent: () => userAgent,
			setUserAgent: (ua: string, languages?: string) => calls.push([ua, languages]),
			webRequest: { onBeforeSendHeaders: (listener: HeadersListener) => listeners.push(listener) },
		},
		send(url: string, requestHeaders: Record<string, string> = {}): Record<string, string> {
			let sent: Record<string, string> = {};
			listeners.at(-1)?.({ url, requestHeaders }, (response) => (sent = response.requestHeaders));
			return sent;
		},
	};
}

describe("browserUserAgent", () => {
	it("drops the app token, keeps the Electron token and reduces the Chrome version", () => {
		expect(browserUserAgent(ELECTRON_MAC_UA)).toBe(
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Electron/33.4.11 Safari/537.36",
		);
	});

	it("keeps Chromium's own platform token", () => {
		expect(
			browserUserAgent(
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Agent Orchestrator/1.0.0 Chrome/144.0.7559.173 Electron/40.5.0 Safari/537.36",
			),
		).toBe("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Electron/40.5.0 Safari/537.36");
	});
});

describe("applyUserAgentFallback", () => {
	it("gives the process-wide fallback the same user agent as the Browser sessions", () => {
		const target = { userAgentFallback: ELECTRON_MAC_UA };
		const fake = fakeSession();
		applyBrowserIdentity(fake.session, { chromeVersion: "130.0.6723.191", platform: "darwin", languages: "en-GB" });
		applyUserAgentFallback(target);
		expect(target.userAgentFallback).toBe(fake.calls[0]?.[0]);
	});

	// Without this call Cloudflare sign-in fails in the Browser panel, and no
	// other test notices.
	it("runs at startup, before any window exists", () => {
		const main = readFileSync(path.join(process.cwd(), "src/main.ts"), "utf8");
		expect(main).toMatch(/app\.whenReady\(\)\.then\(async \(\) => \{\s*applyUserAgentFallback\(app\);/);
	});

	it("does not hold up the window for the profile cookie repair", () => {
		const main = readFileSync(path.join(process.cwd(), "src/main.ts"), "utf8");
		expect(main).toMatch(/void repairBrowserProfileCookies\(/);
		expect(main).not.toMatch(/await repairBrowserProfileCookies\(/);
	});

	it("is stable when applied twice", () => {
		const target = { userAgentFallback: ELECTRON_MAC_UA };
		applyUserAgentFallback(target);
		const once = target.userAgentFallback;
		applyUserAgentFallback(target);
		expect(target.userAgentFallback).toBe(once);
	});
});

describe("chromiumBrands", () => {
	// Chromium's GREASE brand and order are seeded by the major version
	// (components/embedder_support/user_agent_utils.cc). 130 and 152 are the
	// values Electron 33 and 44 report in navigator.userAgentData.brands.
	it("matches the brand list Chromium exposes to pages", () => {
		expect(chromiumBrands(130)).toEqual([
			{ brand: "Not?A_Brand", version: "99" },
			{ brand: "Chromium", version: "130" },
		]);
		expect(chromiumBrands(152)).toEqual([
			{ brand: "Not?A_Brand", version: "24" },
			{ brand: "Chromium", version: "152" },
		]);
		expect(chromiumBrands(131)).toEqual([
			{ brand: "Chromium", version: "131" },
			{ brand: "Not_A Brand", version: "24" },
		]);
	});
});

describe("clientHintHeaders", () => {
	it("serializes the low-entropy hints Chrome sends on every secure request", () => {
		expect(clientHintHeaders(130, "darwin")).toEqual({
			"sec-ch-ua": '"Not?A_Brand";v="99", "Chromium";v="130"',
			"sec-ch-ua-mobile": "?0",
			"sec-ch-ua-platform": '"macOS"',
		});
		expect(clientHintHeaders(130, "win32")["sec-ch-ua-platform"]).toBe('"Windows"');
		expect(clientHintHeaders(130, "linux")["sec-ch-ua-platform"]).toBe('"Linux"');
	});
});

describe("sendsClientHints", () => {
	it("covers only potentially trustworthy http(s) URLs", () => {
		expect(sendsClientHints("https://example.com/")).toBe(true);
		expect(sendsClientHints("http://localhost:5173/")).toBe(true);
		expect(sendsClientHints("http://app.localhost/")).toBe(true);
		expect(sendsClientHints("http://127.0.0.1:8080/")).toBe(true);
		expect(sendsClientHints("http://[::1]/")).toBe(true);
		expect(sendsClientHints("http://example.com/")).toBe(false);
		expect(sendsClientHints("wss://example.com/")).toBe(false);
		expect(sendsClientHints("not a url")).toBe(false);
	});
});

describe("acceptLanguages", () => {
	it("lists the same languages navigator.languages reports, once each", () => {
		expect(acceptLanguages("en-GB", ["en-IN", "en-GB"])).toBe("en-GB,en-IN");
		expect(acceptLanguages("", [])).toBe("en-US");
	});
});

describe("applyBrowserIdentity", () => {
	const identity = { chromeVersion: "130.0.6723.191", platform: "darwin" as const, languages: "en-GB,en-IN" };

	it("sets a Chrome-shaped user agent and the page's languages on the session", () => {
		const fake = fakeSession();
		applyBrowserIdentity(fake.session, identity);
		expect(fake.calls).toEqual([
			[
				"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Electron/33.4.11 Safari/537.36",
				"en-GB,en-IN",
			],
		]);
	});

	it("adds client hints to secure requests only", () => {
		const fake = fakeSession();
		applyBrowserIdentity(fake.session, identity);
		expect(fake.send("https://example.com/", { Accept: "*/*" })).toEqual({
			Accept: "*/*",
			"sec-ch-ua": '"Not?A_Brand";v="99", "Chromium";v="130"',
			"sec-ch-ua-mobile": "?0",
			"sec-ch-ua-platform": '"macOS"',
		});
		expect(fake.send("http://example.com/", { Accept: "*/*" })).toEqual({ Accept: "*/*" });
	});

	it("keeps a client hint Chromium already set", () => {
		const fake = fakeSession();
		applyBrowserIdentity(fake.session, identity);
		expect(fake.send("https://example.com/", { "Sec-CH-UA-Mobile": "?1" })).toEqual({
			"Sec-CH-UA-Mobile": "?1",
			"sec-ch-ua": '"Not?A_Brand";v="99", "Chromium";v="130"',
			"sec-ch-ua-platform": '"macOS"',
		});
	});

	it("configures each session once", () => {
		const fake = fakeSession();
		applyBrowserIdentity(fake.session, identity);
		applyBrowserIdentity(fake.session, identity);
		expect(fake.calls).toHaveLength(1);
		expect(fake.listeners).toHaveLength(1);
	});
});
