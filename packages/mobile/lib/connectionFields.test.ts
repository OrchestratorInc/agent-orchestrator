import { describe, expect, it } from "vitest";
import { manualAddressError, resolveManualAddress } from "./connectionFields";

describe("resolveManualAddress", () => {
	it("keeps a plain host and port", () => {
		expect(resolveManualAddress("192.168.1.42", "3011", false)).toEqual({ ok: true, host: "192.168.1.42", port: "3011", secure: false });
		expect(resolveManualAddress("  my-pc.tail1234.ts.net ", " 3011 ", true)).toEqual({ ok: true, host: "my-pc.tail1234.ts.net", port: "3011", secure: true });
	});

	it("takes the port from host:port when Port is empty", () => {
		expect(resolveManualAddress("example.trycloudflare.com:443", "", true)).toEqual({ ok: true, host: "example.trycloudflare.com", port: "443", secure: true });
		expect(resolveManualAddress("192.168.1.42:3011", "", false)).toEqual({ ok: true, host: "192.168.1.42", port: "3011", secure: false });
	});

	it("accepts the port in both fields when they agree", () => {
		expect(resolveManualAddress("example.trycloudflare.com:443", "443", true)).toEqual({ ok: true, host: "example.trycloudflare.com", port: "443", secure: true });
	});

	it("reads scheme://host:port, with a trailing slash", () => {
		expect(resolveManualAddress("https://example.trycloudflare.com:443/", "443", true)).toEqual({ ok: true, host: "example.trycloudflare.com", port: "443", secure: true });
		expect(resolveManualAddress("https://example.trycloudflare.com:443", "", true)).toEqual({ ok: true, host: "example.trycloudflare.com", port: "443", secure: true });
		expect(resolveManualAddress("http://192.168.1.42:3011", "", false)).toEqual({ ok: true, host: "192.168.1.42", port: "3011", secure: false });
	});

	it("reads bracketed IPv6, with and without a port, and leaves a bare one as typed", () => {
		expect(resolveManualAddress("[::1]:8080", "", false)).toEqual({ ok: true, host: "[::1]", port: "8080", secure: false });
		expect(resolveManualAddress("http://[fe80::1]:3011/", "", false)).toEqual({ ok: true, host: "[fe80::1]", port: "3011", secure: false });
		expect(resolveManualAddress("[::1]", "3011", false)).toEqual({ ok: true, host: "[::1]", port: "3011", secure: false });
		expect(resolveManualAddress("fe80::1", "3011", false)).toEqual({ ok: true, host: "fe80::1", port: "3011", secure: false });
	});

	it("falls back to 443 with TLS on, and otherwise waits for a port", () => {
		expect(resolveManualAddress("example.trycloudflare.com", "", true)).toEqual({ ok: true, host: "example.trycloudflare.com", port: "443", secure: true });
		expect(resolveManualAddress("https://example.trycloudflare.com", "", true)).toEqual({ ok: true, host: "example.trycloudflare.com", port: "443", secure: true });
		expect(resolveManualAddress("192.168.1.42", "", false)).toEqual({ ok: false, incomplete: true });
		expect(manualAddressError("192.168.1.42", "", false)).toBeUndefined();
	});

	it("is incomplete, not an error, while the host is empty", () => {
		expect(resolveManualAddress("", "3011", false)).toEqual({ ok: false, incomplete: true });
		expect(resolveManualAddress("https://", "", true)).toEqual({ ok: false, incomplete: true });
		expect(manualAddressError("", "3011", false)).toBeUndefined();
		expect(manualAddressError("", "abc", false)).toBe("Enter a port from 1 to 65535.");
	});

	it("says so when the host and Port disagree", () => {
		const message = "The host says port 443 but Port is 3011. Make them match, or clear one.";
		expect(resolveManualAddress("https://example.trycloudflare.com:443", "3011", true)).toEqual({ ok: false, error: message });
		expect(manualAddressError("example.trycloudflare.com:443", "3011", true)).toBe(message);
		expect(resolveManualAddress("[::1]:8080", "3011", false)).toMatchObject({ ok: false, error: expect.stringContaining("port 8080") });
	});

	it("rejects bad ports and a scheme that contradicts the TLS toggle", () => {
		expect(resolveManualAddress("example.com:99999", "", false)).toMatchObject({ ok: false, error: expect.stringContaining("port in the host") });
		expect(resolveManualAddress("example.com:", "", false)).toMatchObject({ ok: false, error: expect.stringContaining("port in the host") });
		expect(resolveManualAddress(":443", "", true)).toMatchObject({ ok: false, error: "Enter a host before the port." });
		expect(resolveManualAddress("example.com", "70000", false)).toEqual({ ok: false, error: "Enter a port from 1 to 65535." });
		expect(manualAddressError("https://example.com", "443", false)).toBe("The host starts with https://. Turn on Use TLS, or remove https://.");
		expect(manualAddressError("http://example.com", "80", true)).toBe("The host starts with http://. Turn off Use TLS, or remove http://.");
	});
});
