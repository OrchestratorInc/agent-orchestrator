import { afterEach, expect, it, vi } from "vitest";
import { captureExceptionToSentry } from "./sentry";

const capture = vi.hoisted(() => vi.fn());
vi.mock("./bridge", () => ({ aoBridge: { telemetry: { capture } } }));

afterEach(() => vi.clearAllMocks());

it("sends only safe renderer stack frames and a standard exception type through the bridge", () => {
	const error = new TypeError("resume agent ai-sports-coach-113");
	error.stack = [
		"TypeError: resume agent ai-sports-coach-113",
		"    at renderWidget (app://renderer/assets/app-A1B2C3D4.js:42:7)",
		"    at loadState (file:///Users/alice/private-repo/state.ts:4:2)",
		"    at updateView (http://localhost:5173/src/View.tsx:13:4)",
		"    at private-repo (app://renderer/assets/app-A1B2C3D4.js:25:3)",
		"    at privateProject (app://renderer/assets/app-A1B2C3D4.js:26:3)",
	].join("\n");
	captureExceptionToSentry(error, { category: "render_crash" });
	expect(capture).toHaveBeenCalledWith(expect.objectContaining({
		kind: "exception",
		exception: { type: "TypeError", frames: [{ function: "renderer_frame", filename: "assets/app-A1B2C3D4.js", lineno: 26 }, { function: "updateView", lineno: 13 }, { function: "renderWidget", filename: "assets/app-A1B2C3D4.js", lineno: 42 }] },
	}));
	expect(JSON.stringify(capture.mock.calls[0]?.[0].exception)).not.toMatch(/private-repo|privateProject|\/Users\/alice|app:\/\/|localhost/);
});
