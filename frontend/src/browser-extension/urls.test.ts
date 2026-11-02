import { describe, expect, it } from "vitest";
import { normalizeAgentURL, originOf } from "./urls";

describe("normalizeAgentURL", () => {
	it("accepts http(s), localhost and hostnames", () => {
		expect(normalizeAgentURL("https://example.com/a?b=1")).toBe("https://example.com/a?b=1");
		expect(normalizeAgentURL("localhost:5173")).toBe("http://localhost:5173/");
		expect(normalizeAgentURL("127.0.0.1:8080/x")).toBe("http://127.0.0.1:8080/x");
		expect(normalizeAgentURL("example.com")).toBe("https://example.com/");
	});

	it("rejects files, other schemes and search terms", () => {
		for (const [input, code] of [
			["/etc/passwd", "BROWSER_URL_FORBIDDEN"],
			["file:///etc/passwd", "BROWSER_URL_FORBIDDEN"],
			["C:\\Windows", "BROWSER_URL_FORBIDDEN"],
			["javascript:alert(1)", "BROWSER_URL_FORBIDDEN"],
			["chrome://settings", "BROWSER_URL_FORBIDDEN"],
			["how to bake bread", "INVALID_URL"],
			["  ", "URL_REQUIRED"],
		]) {
			expect(() => normalizeAgentURL(input!)).toThrow(expect.objectContaining({ code }));
		}
	});

	it("derives web origins only", () => {
		expect(originOf("https://example.com/a")).toBe("https://example.com");
		expect(originOf("about:blank")).toBe("");
		expect(originOf(undefined)).toBe("");
	});
});
