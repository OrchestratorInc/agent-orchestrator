import { describe, expect, it, vi } from "vitest";
import { allowDevRendererToFrame, installDevRendererFraming } from "./dev-frame-ancestors";

const STAGING_POLICY =
	"sandbox allow-scripts allow-forms; default-src 'self' data: blob:; connect-src 'none'; frame-ancestors https://staging-api.aoagents.dev app://renderer";

describe("allowDevRendererToFrame", () => {
	it("adds the dev origin where the packaged app may frame the page", () => {
		expect(allowDevRendererToFrame(STAGING_POLICY, "http://localhost:5173")).toBe(
			"sandbox allow-scripts allow-forms; default-src 'self' data: blob:; connect-src 'none'; frame-ancestors https://staging-api.aoagents.dev app://renderer http://localhost:5173",
		);
	});

	it("leaves a page the packaged app may not frame alone", () => {
		const policy = "default-src 'none'; frame-ancestors 'none'";
		expect(allowDevRendererToFrame(policy, "http://localhost:5173")).toBe(policy);
		expect(allowDevRendererToFrame("default-src 'self'", "http://localhost:5173")).toBe("default-src 'self'");
	});

	it("does not add the dev origin twice", () => {
		const once = allowDevRendererToFrame(STAGING_POLICY, "http://localhost:5173");
		expect(allowDevRendererToFrame(once, "http://localhost:5173")).toBe(once);
	});
});

describe("installDevRendererFraming", () => {
	type Listener = (details: Record<string, unknown>, callback: (response: Record<string, unknown>) => void) => void;
	function install() {
		let listener: Listener | undefined;
		const session = { webRequest: { onHeadersReceived: vi.fn((_filter: unknown, fn: Listener) => { listener = fn; }) } };
		installDevRendererFraming(session as never, "http://localhost:5173/");
		return (details: Record<string, unknown>) => {
			const callback = vi.fn();
			listener?.(details, callback);
			return callback.mock.calls[0]?.[0] as Record<string, unknown>;
		};
	}

	it("rewrites only a subframe's policy", () => {
		const run = install();
		const headers = { "content-security-policy": [STAGING_POLICY], "content-type": ["text/html"] };
		expect(run({ resourceType: "subFrame", responseHeaders: headers })).toEqual({
			responseHeaders: {
				"content-security-policy": [`${STAGING_POLICY} http://localhost:5173`],
				"content-type": ["text/html"],
			},
		});
		expect(run({ resourceType: "mainFrame", responseHeaders: headers })).toEqual({});
		expect(run({ resourceType: "subFrame", responseHeaders: { "content-type": ["text/html"] } })).toEqual({});
	});
});
