import { describe, expect, it } from "vitest";
import { hostLoopbackUrl } from "./loopback";

describe("hostLoopbackUrl", () => {
	it.each([
		["localhost:5173/app", "http://localhost:5173/app"],
		["http://127.0.0.2:8080/", "http://127.0.0.2:8080/"],
		["https://[::1]:8443/x", "https://[::1]:8443/x"],
		["http://0.0.0.0:3000", "http://0.0.0.0:3000/"],
		["http://app.localhost:3000/", "http://app.localhost:3000/"],
		["http://[::ffff:127.0.0.1]:3000/", "http://[::ffff:7f00:1]:3000/"],
	])("takes %s as the host's own address", (target, url) => {
		expect(hostLoopbackUrl(target)).toBe(url);
	});

	it.each(["https://example.com/", "http://ao-preview-abc.localhost:4000/", "https://ao-preview-artifact.abc.localhost:4000/a.html", "file:///tmp/index.html", "localhost.example.com:3000"])("leaves %s to the preview proxy", (target) => {
		expect(hostLoopbackUrl(target)).toBeUndefined();
	});
});
