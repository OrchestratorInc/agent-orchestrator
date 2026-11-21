import { describe, expect, it } from "vitest";
import { hostLoopbackUrl, ipcErrorReason } from "./loopback";

describe("hostLoopbackUrl", () => {
	it.each([
		["localhost:5173/app", "http://localhost:5173/app"],
		["http://127.0.0.2:8080/", "http://127.0.0.2:8080/"],
		["https://[::1]:8443/x", "https://[::1]:8443/x"],
		["http://0.0.0.0:3000", "http://0.0.0.0:3000/"],
	])("takes %s as the host's own address", (target, url) => {
		expect(hostLoopbackUrl(target)).toBe(url);
	});

	it.each(["https://example.com/", "http://ao-preview-abc.localhost:4000/", "file:///tmp/index.html", "localhost.example.com:3000"])("leaves %s to the preview proxy", (target) => {
		expect(hostLoopbackUrl(target)).toBeUndefined();
	});
});

describe("ipcErrorReason", () => {
	it("drops the IPC wrapper from a main-process error", () => {
		expect(ipcErrorReason(new Error("Error invoking remote method 'remotes:externalUrl': Error: Can't reach this host's localhost."))).toBe("Can't reach this host's localhost.");
		expect(ipcErrorReason(new Error("plain"))).toBe("plain");
		expect(ipcErrorReason("nope")).toBeUndefined();
	});
});
