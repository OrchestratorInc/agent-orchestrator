import { describe, expect, it } from "vitest";
import { endpointKindForHost, isTunnelHost, manualAddressError, resolveManualAddress } from "./connectionFields";

const FIXED = "h-0123456789abcdef0123.useao.dev";
const tls = (port = "443") => ({ port, secure: true });
const plain = (port = "3011") => ({ port, secure: false });

describe("resolveManualAddress", () => {
	describe("decides TLS from what was typed", () => {
		it.each([
			// A typed scheme wins over everything else.
			["https://example.com", "", [tls()]],
			["https://192.168.1.42:3011", "", [tls("3011")]],
			["wss://example.com", "", [tls()]],
			["http://example.com", "", [plain()]],
			["http://my-pc.tail1234.ts.net:8080", "", [plain("8080")]],
			["http://example.trycloudflare.com", "", [plain()]],
			// Tunnel names are only ever served over TLS on 443.
			["example.trycloudflare.com", "", [tls()]],
			[FIXED, "", [tls()]],
			[`${FIXED}:443`, "", [tls()]],
			// Port 443 is TLS.
			["example.com", "443", [tls()]],
			["192.168.1.42:443", "", [tls()]],
			// Addresses that only exist on this network or tailnet are plain HTTP.
			["192.168.1.42", "3011", [plain()]],
			["10.0.0.5", "", [plain()]],
			["172.16.4.2", "", [plain()]],
			["169.254.1.1", "", [plain()]],
			["127.0.0.1", "", [plain()]],
			["localhost", "", [plain()]],
			["my-mac.local", "", [plain()]],
			["my-pc", "", [plain()]],
			["100.101.102.103", "", [plain()]],
			["[::1]:8080", "", [plain("8080")]],
			["fe80::1", "3011", [plain()]],
			["[fd12:3456::1]", "", [plain()]],
			// A tailnet name without a port is Tailscale Serve's HTTPS address.
			["my-pc.tail1234.ts.net", "", [tls()]],
			// Anything else gets both, TLS first.
			["my-pc.tail1234.ts.net", "3011", [tls("3011"), plain("3011")]],
			["ao.example.com", "", [tls(), plain()]],
			["nas.lan", "8443", [tls("8443"), plain("8443")]],
			["203.0.113.7", "", [tls(), plain()]],
			["[2001:db8::1]", "", [tls(), plain()]],
		])("%s with Port %j", (host, port, routes) => {
			expect(resolveManualAddress(host, port)).toEqual({ ok: true, host: expect.any(String), routes });
		});
	});

	it("keeps the host, without its scheme, port or trailing slash", () => {
		expect(resolveManualAddress("  https://example.trycloudflare.com:443/ ", "")).toMatchObject({ host: "example.trycloudflare.com" });
		expect(resolveManualAddress("http://[fe80::1]:3011/", "")).toMatchObject({ host: "[fe80::1]", routes: [plain()] });
		expect(resolveManualAddress("fe80::1", "3011")).toMatchObject({ host: "fe80::1" });
	});

	it("accepts the port in both fields when they agree", () => {
		expect(resolveManualAddress("example.trycloudflare.com:443", "443")).toEqual({ ok: true, host: "example.trycloudflare.com", routes: [tls()] });
		expect(resolveManualAddress("192.168.1.42:3011", "03011")).toEqual({ ok: true, host: "192.168.1.42", routes: [plain()] });
	});

	describe("for a machine being edited", () => {
		const saved = { host: "203.0.113.7", port: 8080, secure: false };

		it("keeps the saved TLS choice while the address still matches", () => {
			expect(resolveManualAddress("203.0.113.7", "8080", saved)).toEqual({ ok: true, host: "203.0.113.7", routes: [plain("8080")] });
			expect(resolveManualAddress("203.0.113.7", "", saved)).toEqual({ ok: true, host: "203.0.113.7", routes: [plain("8080")] });
			const tunnel = { host: "ao.example.com", port: 443, secure: true };
			expect(resolveManualAddress("AO.example.com", "443", tunnel)).toEqual({ ok: true, host: "AO.example.com", routes: [tls()] });
		});

		it("decides again once the address or port changes", () => {
			expect(resolveManualAddress("203.0.113.8", "8080", saved)).toEqual({ ok: true, host: "203.0.113.8", routes: [tls("8080"), plain("8080")] });
			expect(resolveManualAddress("203.0.113.7", "9090", saved)).toEqual({ ok: true, host: "203.0.113.7", routes: [tls("9090"), plain("9090")] });
		});

		it("lets a typed scheme override the saved choice", () => {
			expect(resolveManualAddress("https://203.0.113.7:8080", "", saved)).toEqual({ ok: true, host: "203.0.113.7", routes: [tls("8080")] });
		});
	});

	it("is incomplete, not an error, while the host is empty", () => {
		expect(resolveManualAddress("", "3011")).toEqual({ ok: false, incomplete: true });
		expect(resolveManualAddress("https://", "")).toEqual({ ok: false, incomplete: true });
		expect(manualAddressError("", "3011")).toBeUndefined();
		expect(manualAddressError("", "abc")).toBe("Enter a port from 1 to 65535.");
	});

	it("says so when the host and Port disagree", () => {
		const message = "The host says port 443 but Port is 3011. Make them match, or clear one.";
		expect(resolveManualAddress("https://example.trycloudflare.com:443", "3011")).toEqual({ ok: false, error: message });
		expect(manualAddressError("example.trycloudflare.com:443", "3011")).toBe(message);
		expect(resolveManualAddress("[::1]:8080", "3011")).toMatchObject({ ok: false, error: expect.stringContaining("port 8080") });
	});

	it("rejects bad ports, and a tunnel name on a port it is never served on", () => {
		expect(resolveManualAddress("example.com:99999", "")).toMatchObject({ ok: false, error: expect.stringContaining("port in the host") });
		expect(resolveManualAddress("example.com:", "")).toMatchObject({ ok: false, error: expect.stringContaining("port in the host") });
		expect(resolveManualAddress(":443", "")).toMatchObject({ ok: false, error: "Enter a host before the port." });
		expect(resolveManualAddress("example.com", "70000")).toEqual({ ok: false, error: "Enter a port from 1 to 65535." });
		expect(manualAddressError(FIXED, "3011")).toBe("Tunnel addresses use port 443. Clear Port or enter 443.");
		expect(manualAddressError("192.168.1.42", "")).toBeUndefined();
	});
});

describe("isTunnelHost", () => {
	it("knows quick tunnels and fixed addresses, and nothing else", () => {
		expect(isTunnelHost("abc.trycloudflare.com")).toBe(true);
		expect(isTunnelHost(FIXED.toUpperCase())).toBe(true);
		expect(isTunnelHost("h-123.useao.dev")).toBe(false);
		expect(isTunnelHost("ao.example.com")).toBe(false);
	});
});

describe("endpointKindForHost", () => {
	it("labels a route by its name, as the endpoint race ranks it", () => {
		expect(endpointKindForHost("abc.trycloudflare.com", true)).toBe("tunnel");
		expect(endpointKindForHost(FIXED, true)).toBe("tunnel");
		expect(endpointKindForHost(FIXED, false)).toBe("lan");
		expect(endpointKindForHost("my-pc.tail1234.ts.net", true)).toBe("tailscale");
		expect(endpointKindForHost("100.101.102.103", false)).toBe("tailscale");
		expect(endpointKindForHost("ao.example.com", true)).toBe("lan");
		expect(endpointKindForHost("192.168.1.42", false)).toBe("lan");
	});
});
