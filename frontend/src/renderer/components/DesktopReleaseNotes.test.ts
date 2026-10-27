import { expect, it } from "vitest";
import { prepareDesktopReleaseNotes } from "../lib/desktop-release-notes";
import { normalizeReleaseNotes } from "../../shared/update-telemetry";

it("removes contributor handles and the comparison footer while retaining linked PR numbers", () => {
	const notes = [
		"### Added",
		"",
		"- Add useful workflows by @person in [#123](https://github.com/Untrivial-ai/agent-orchestrator/pull/123)",
		"- Update dependencies by @dependabot[bot] in [#124](https://github.com/Untrivial-ai/agent-orchestrator/pull/124)",
		"",
		"**Full Changelog**: https://github.com/Untrivial-ai/agent-orchestrator/compare/v0.13.0...v0.13.1",
	].join("\n");

	expect(prepareDesktopReleaseNotes(notes)).toBe([
		"### Added",
		"",
		"- Add useful workflows [#123](https://github.com/Untrivial-ai/agent-orchestrator/pull/123)",
		"- Update dependencies [#124](https://github.com/Untrivial-ai/agent-orchestrator/pull/124)",
	].join("\n"));
});

it("leaves nightly notes and unrelated user text unchanged", () => {
	const notes = "This automated build tracks main.\n\n- Source: @team in #general";
	expect(prepareDesktopReleaseNotes(notes)).toBe(notes);
});

it("removes contributor handles from generated nightly changes", () => {
	const notes = [
		"**Changes in this nightly**",
		"",
		"- Add durable scheduled automations by @person in [#4459](https://github.com/Untrivial-ai/agent-orchestrator/pull/4459)",
		"",
		"**Build details**",
	].join("\n");

	expect(prepareDesktopReleaseNotes(notes)).toBe([
		"**Changes in this nightly**",
		"",
		"- Add durable scheduled automations [#4459](https://github.com/Untrivial-ai/agent-orchestrator/pull/4459)",
		"",
		"**Build details**",
	].join("\n"));
});

it("cleans release notes published after the repository moved to OrchestratorInc", () => {
	const notes = [
		"**Changes in this nightly**",
		"",
		"- Fix update links by @person in [#6228](https://github.com/OrchestratorInc/agent-orchestrator/pull/6228)",
		"",
		"**Full Changelog**: https://github.com/OrchestratorInc/agent-orchestrator/compare/v0.13.3...v0.13.4",
	].join("\n");

	expect(prepareDesktopReleaseNotes(notes)).toBe([
		"**Changes in this nightly**",
		"",
		"- Fix update links [#6228](https://github.com/OrchestratorInc/agent-orchestrator/pull/6228)",
	].join("\n"));
});

// releases.atom delivers the same body as HTML; after normalization the
// renderer cleanup must give the same result as for the Markdown body.
it("cleans HTML feed notes the same way as the Markdown release body", () => {
	const pull = "https://github.com/OrchestratorInc/agent-orchestrator/pull/124";
	const compare = "https://github.com/OrchestratorInc/agent-orchestrator/compare/v0.14.2...v0.14.3";
	const html = [
		"<h3>Fixed</h3>",
		"<ul>",
		`<li>Update dependencies by <a class="user-mention notranslate" href="https://github.com/apps/dependabot">@dependabot[bot]</a> in <a href="${pull}">#124</a></li>`,
		"</ul>",
		`<p><strong>Full Changelog</strong>: <a class="commit-link" href="${compare}"><tt>v0.14.2...v0.14.3</tt></a></p>`,
	].join("\n");
	const markdown = [
		"### Fixed",
		"",
		`- Update dependencies by @dependabot[bot] in [#124](${pull})`,
		"",
		`**Full Changelog**: ${compare}`,
	].join("\n");

	const expected = `### Fixed\n\n- Update dependencies [#124](${pull})`;
	expect(prepareDesktopReleaseNotes(normalizeReleaseNotes(html) ?? "")).toBe(expected);
	expect(prepareDesktopReleaseNotes(normalizeReleaseNotes(markdown) ?? "")).toBe(expected);
});
