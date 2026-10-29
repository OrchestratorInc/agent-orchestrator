import { describe, expect, it } from "vitest";
import {
	displayableUpdateError,
	isManifest404Error,
	looksLikeRawUpdateError,
	userFacingUpdateError,
} from "./update-error-message";

const FEED = "https://github.com/AgentWrapper/agent-orchestrator/releases/latest";

// The shape from #5755: GitHub answers /releases/latest with a 504, and the
// provider wraps that HttpError's stack inside "Cannot parse releases feed",
// so one message carries the HTML body, the header block and app.asar paths.
function releasesFeedDump(status = "504 Gateway Time-out"): Error {
	const err = new Error(
		`Cannot parse releases feed: Error: Unable to find latest version on GitHub (${FEED}), please ensure a production release exists: HttpError: ${status}\n` +
			`"method: GET url: ${FEED}\\n\\n<!DOCTYPE html>\\n<html><head><title>Unicorn!</title></head><body>No server is currently available</body></html>\\n"\n` +
			'Headers: {\n  "set-cookie": "_gh_sess=abc123; path=/; secure; HttpOnly",\n  "x-ratelimit-limit": "60"\n}\n' +
			"    at createHttpError (C:\\Users\\someone\\AppData\\Local\\Programs\\agent-orchestrator\\resources\\app.asar\\node_modules\\builder-util-runtime\\out\\httpExecutor.js:53:12)\n" +
			"    at ElectronHttpExecutor.handleResponse (C:\\Users\\someone\\AppData\\Local\\Programs\\agent-orchestrator\\resources\\app.asar\\node_modules\\builder-util-runtime\\out\\httpExecutor.js:150:21),\n" +
			'XML:\n<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"></feed>',
	);
	return Object.assign(err, { code: "ERR_UPDATER_INVALID_RELEASE_FEED" });
}

describe("userFacingUpdateError", () => {
	it("turns the #5755 gateway-timeout dump into one sentence", () => {
		const message = userFacingUpdateError(releasesFeedDump(), "check");

		expect(message).toBe("Couldn't check for updates — the update server timed out. Try again in a few minutes.");
		expect(looksLikeRawUpdateError(message)).toBe(false);
	});

	it("starts the sentence with the operation that failed", () => {
		expect(userFacingUpdateError(releasesFeedDump(), "download")).toBe(
			"Download failed — the update server timed out. Try again in a few minutes.",
		);
		expect(userFacingUpdateError(releasesFeedDump(), "install")).toBe(
			"Couldn't install the update — the update server timed out. Try again in a few minutes.",
		);
	});

	it.each([
		["502 Bad Gateway", "the update server isn't available right now. Try again in a few minutes."],
		["503 Service Unavailable", "the update server isn't available right now. Try again in a few minutes."],
		["429 Too Many Requests", "the update server is limiting requests. Try again later."],
		["403 Forbidden\nAPI rate limit exceeded for 203.0.113.7.", "the update server is limiting requests. Try again later."],
		["403 Forbidden", "the update server returned an error (HTTP 403). Try again later."],
		["404 Not Found", "the update information was not found on the server."],
	])("explains an HTTP %s", (status, reason) => {
		expect(userFacingUpdateError(releasesFeedDump(status), "check")).toBe(`Couldn't check for updates — ${reason}`);
	});

	it("reads the status off an unwrapped HttpError", () => {
		const err = Object.assign(new Error("503 Service Unavailable\nHeaders: {}"), { name: "HttpError", statusCode: 503 });

		expect(userFacingUpdateError(err, "download")).toBe(
			"Download failed — the update server isn't available right now. Try again in a few minutes.",
		);
	});

	it("keeps the manifest-404 wording AO already uses", () => {
		const err = new Error(
			'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
		);

		expect(isManifest404Error(err)).toBe(true);
		expect(userFacingUpdateError(err, "check")).toBe(
			"Couldn't check for updates — the update information was not found on the server.",
		);
		expect(userFacingUpdateError(err, "download")).toBe("Download failed — the update file was not found on the server.");
	});

	it("explains a feed that arrived but could not be read", () => {
		const err = Object.assign(new Error("Cannot parse releases feed: Error: Unexpected close tag\nLine: 3\n,\nXML:\n<html>"), {
			code: "ERR_UPDATER_INVALID_RELEASE_FEED",
		});

		expect(userFacingUpdateError(err, "check")).toBe(
			"Couldn't check for updates — the update information from the server couldn't be read. Try again in a few minutes.",
		);
	});

	it("explains a feed with no release", () => {
		const err = Object.assign(new Error("No published versions on GitHub"), { code: "ERR_UPDATER_NO_PUBLISHED_VERSIONS" });

		expect(userFacingUpdateError(err, "check")).toBe(
			"Couldn't check for updates — the update information was not found on the server.",
		);
	});

	it("explains a network failure the provider wrapped", () => {
		const err = new Error(
			`Unable to find latest version on GitHub (${FEED}), please ensure a production release exists: Error: net::ERR_CONNECTION_RESET\n    at SimpleURLLoaderWrapper.<anonymous> (node:electron/js2c/browser_init:2:115436)`,
		);

		expect(userFacingUpdateError(err, "check")).toBe(
			"Couldn't check for updates — the update server couldn't be reached. Check your connection and try again.",
		);
	});

	// The renderer swaps these for localized restart guidance off netError (#3526).
	it("passes a bare net:: failure through", () => {
		expect(userFacingUpdateError(new Error("net::ERR_INTERNET_DISCONNECTED"), "check")).toBe("net::ERR_INTERNET_DISCONNECTED");
	});

	it("passes AO's own sentences through", () => {
		const own = "The remembered update is no longer available. Check for updates and try again.";

		expect(userFacingUpdateError(new Error(own), "install")).toBe(own);
		expect(userFacingUpdateError(own, "install")).toBe(own);
	});

	it("falls back to a generic line for a dump it does not recognize", () => {
		const err = new Error("Something odd\n    at Object.<anonymous> (/Applications/Agent Orchestrator.app/Contents/Resources/app.asar/main.js:1:1)");

		expect(userFacingUpdateError(err, "download")).toBe("Download failed. Try again in a few minutes.");
	});

	it("falls back to a generic line when there is no message", () => {
		expect(userFacingUpdateError(undefined, "check")).toBe("Couldn't check for updates. Try again in a few minutes.");
		expect(userFacingUpdateError(null, "install")).toBe("Couldn't install the update. Try again in a few minutes.");
		expect(userFacingUpdateError(new Error(""), "download")).toBe("Download failed. Try again in a few minutes.");
	});
});

describe("displayableUpdateError", () => {
	it("keeps a sentence and replaces a dump", () => {
		expect(displayableUpdateError("Not enough disk space to install the update.", "Update failed.")).toBe(
			"Not enough disk space to install the update.",
		);
		expect(displayableUpdateError(releasesFeedDump().message, "Update failed.")).toBe("Update failed.");
		expect(displayableUpdateError(undefined, "Update failed.")).toBe("Update failed.");
		expect(displayableUpdateError("", "Update failed.")).toBe("Update failed.");
	});

	it.each([
		["an HTML body", "<html><body>Bad gateway</body></html>"],
		["a header block", 'Headers: {"content-type": "text/html"}'],
		["a cookie", "set-cookie: _gh_sess=abc123"],
		["a stack frame", "Failed\n    at fn (file.js:1:1)"],
		["an app.asar path", "Cannot open C:\\AO\\resources\\app.asar\\main.js"],
		["an electron-updater code", "ERR_UPDATER_INVALID_UPDATE_INFO"],
		["unbounded text", "x".repeat(1001)],
	])("treats %s as a dump", (_label, text) => {
		expect(looksLikeRawUpdateError(text)).toBe(true);
	});
});
