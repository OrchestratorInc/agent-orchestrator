import { expect, test } from "vitest";
import { isNetErrorMessage, normalizeReleaseNotes, updateFailureCategory, updateFailureOutcome } from "./update-telemetry";

test("detects Chromium network-stack errors", () => {
	expect(isNetErrorMessage("net::ERR_FAILED")).toBe(true);
	expect(isNetErrorMessage("net::ERR_CONNECTION_RESET")).toBe(true);
	// Anchored at the start — a net:: substring elsewhere is not the wedge signature.
	expect(isNetErrorMessage("Error: net::ERR_FAILED")).toBe(false);
	expect(isNetErrorMessage("HttpError: 404")).toBe(false);
	expect(isNetErrorMessage(undefined)).toBe(false);
});

test("buckets updater errors into safe categories", () => {
	expect(updateFailureCategory("net::ERR_CONNECTION_RESET")).toBe("network");
	expect(updateFailureCategory("getaddrinfo ENOTFOUND github.com")).toBe("network");
	expect(updateFailureCategory("sha512 checksum mismatch")).toBe("signature");
	expect(updateFailureCategory("EACCES: permission denied")).toBe("permission");
	expect(updateFailureCategory("ENOSPC: no space left on device")).toBe("disk_space");
	expect(updateFailureCategory("HTTP 404 not found")).toBe("not_found");
	expect(updateFailureCategory("Cannot update: unsupported platform")).toBe("not_supported");
	expect(updateFailureCategory(undefined)).toBe("unknown");
});

// The raw message carries the feed URL and local staging paths, so only the
// bucketed category may leave the process.
test("never forwards the raw updater message", () => {
	const outcome = updateFailureOutcome(
		"EACCES: permission denied, open '/Users/someone/Library/Caches/ao-updater/pending/AO.zip'",
		"download",
		"automatic",
		"0.11.3",
	);
	expect(outcome).toEqual({
		event: "ao.renderer.update_failed",
		phase: "download",
		trigger: "automatic",
		error_category: "permission",
		to_version: "0.11.3",
	});
	expect(JSON.stringify(outcome)).not.toContain("someone");
});

// phase and trigger come from the main process, which is the only place that
// knows whether the hourly timer or the user started the operation.
test("records the phase and trigger it was given", () => {
	expect(updateFailureOutcome("boom", "check", "automatic", undefined)).toMatchObject({
		phase: "check",
		trigger: "automatic",
	});
	expect(updateFailureOutcome("boom", "download", "manual", undefined)).toMatchObject({
		phase: "download",
		trigger: "manual",
	});
	// An unknown target version is omitted rather than sent as undefined.
	expect(updateFailureOutcome("boom", "check", "manual", undefined)).not.toHaveProperty("to_version");
});

// The GitHub provider reads releases.atom, which renders the release body as
// HTML. Flattening it must keep the links the dialog renders from Markdown.
test("keeps links, bullets and headings from GitHub's HTML release notes", () => {
	const html = [
		"<p><strong>Changes in this nightly</strong></p>",
		"<ul>",
		'<li>Avoid inactive warning by <a class="user-mention notranslate" data-hovercard-type="user" href="https://github.com/person">@person</a> in <a href="https://github.com/Untrivial-ai/agent-orchestrator/pull/6972" data-hovercard-type="pull_request">#6972</a></li>',
		'<li>1 more linked change is included in the <a href="https://github.com/Untrivial-ai/agent-orchestrator/compare/v0.14.4-nightly.202610200854...v0.14.4-nightly.202610201803">full comparison</a>.</li>',
		"</ul>",
		"<p><strong>Build details</strong></p>",
		"<ul>",
		"<li>Built: <code>2026-10-20 18:03 UTC</code></li>",
		"</ul>",
	].join("\n");

	expect(normalizeReleaseNotes(html)).toBe([
		"**Changes in this nightly**",
		"",
		"- Avoid inactive warning by @person in [#6972](https://github.com/Untrivial-ai/agent-orchestrator/pull/6972)",
		"- 1 more linked change is included in the [full comparison](https://github.com/Untrivial-ai/agent-orchestrator/compare/v0.14.4-nightly.202610200854...v0.14.4-nightly.202610201803).",
		"",
		"**Build details**",
		"",
		"- Built: `2026-10-20 18:03 UTC`",
	].join("\n"));
});

test("drops links it cannot carry safely as Markdown", () => {
	expect(normalizeReleaseNotes('<p>See <a href="javascript:alert(1)">this</a> and <a href="http://example.com">that</a></p>'))
		.toBe("See this and that");
});

test("leaves Markdown release notes unchanged", () => {
	const markdown = "**Changes**\n\n- Fix it by @person in [#1](https://github.com/Untrivial-ai/agent-orchestrator/pull/1)";
	expect(normalizeReleaseNotes(markdown)).toBe(markdown);
});

test("leaves loose Markdown lists and inline code untouched", () => {
	const markdown = "- First change\n\n- Second change with `a<b>c`";
	expect(normalizeReleaseNotes(markdown)).toBe("- First change\n\n- Second change with `ac`");
});

test("keeps nested HTML list items on their own lines", () => {
	expect(normalizeReleaseNotes("<ul><li>Parent<ul><li>Nested</li></ul></li><li>Sibling</li></ul>"))
		.toBe("- Parent\n- Nested\n- Sibling");
});

test("reads quoted href values even when another attribute contains >", () => {
	expect(normalizeReleaseNotes(
		"<p><a title=\"a > b\" href='https://github.com/Untrivial-ai/agent-orchestrator/pull/1'>#1</a></p>",
	)).toBe("[#1](https://github.com/Untrivial-ai/agent-orchestrator/pull/1)");
});
