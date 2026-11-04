import { afterEach, expect, it, vi } from "vitest";
import { captureApiErrorToSentry, captureExceptionToSentry } from "./sentry";
import { SAFE_APP_ASSET, safeSentryFunctionName } from "../../shared/sentry-config";

const capture = vi.hoisted(() => vi.fn());
vi.mock("./bridge", () => ({ aoBridge: { telemetry: { capture } } }));

afterEach(() => vi.clearAllMocks());

it("keeps real component names and built route assets without accepting arbitrary function text", () => {
	expect(safeSentryFunctionName("ChatWorkspace", "renderer_frame")).toBe("ChatWorkspace");
	expect(safeSentryFunctionName("loadProject", "renderer_frame")).toBe("renderer_frame");
	expect(safeSentryFunctionName("privateUserText", "renderer_frame")).toBe("renderer_frame");
	expect(SAFE_APP_ASSET.test("assets/_shell.index-RzpjvxTk.js")).toBe(true);
	expect(SAFE_APP_ASSET.test("assets/_shell.host._hostId.project._projectId_.session._sessionId-BjAotn5p.js")).toBe(true);
	expect(SAFE_APP_ASSET.test("assets/../private-user-text.js")).toBe(false);
});

it("keeps typed daemon 503 errors as breadcrumbs while reporting untyped 503s", () => {
	captureApiErrorToSentry("GET /api/v1/projects", "http_5xx", 503, "WORKSPACE_GIT_BUSY");
	expect(capture).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "breadcrumb" }));
	captureApiErrorToSentry("GET /api/v1/projects", "http_5xx", 503);
	expect(capture).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "message" }));
});

it("classifies unhandled renderer errors as sampled nonfatal errors", () => {
	captureExceptionToSentry(new TypeError("private"), { unhandled: true });
	expect(capture).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "exception", level: "error" }));
	captureExceptionToSentry(new TypeError("private"), { category: "render_crash", unhandled: true });
	expect(capture).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "exception", level: "fatal" }));
});

it("sends only safe renderer stack frames and a standard exception type through the bridge", () => {
	const error = new TypeError("resume agent ai-sports-coach-113");
	error.stack = [
		"TypeError: resume agent ai-sports-coach-113",
		"    at ChatWorkspace (app://renderer/assets/app-A1B2C3D4.js:42:7)",
		"    at loadState (file:///Users/alice/private-repo/state.ts:4:2)",
		"    at SessionView (http://localhost:5173/src/View.tsx:13:4)",
		"    at private-repo (app://renderer/assets/app-A1B2C3D4.js:25:3)",
		"    at a (app://renderer/assets/_shell-A1B2C3D4.js:26:3)",
	].join("\n");
	captureExceptionToSentry(error, { category: "render_crash" });
	expect(capture).toHaveBeenCalledWith(expect.objectContaining({
		kind: "exception",
		exception: { type: "TypeError", frames: [{ function: "renderer_frame", filename: "assets/_shell-A1B2C3D4.js", lineno: 26, colno: 3 }, { function: "SessionView", lineno: 13, colno: 4 }, { function: "ChatWorkspace", filename: "assets/app-A1B2C3D4.js", lineno: 42, colno: 7 }] },
	}));
	expect(JSON.stringify(capture.mock.calls[0]?.[0].exception)).not.toMatch(/private-repo|\/Users\/alice|app:\/\/|localhost/);
});
