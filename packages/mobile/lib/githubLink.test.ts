import { describe, expect, it } from "vitest";
import { githubAppLink } from "./githubLink";

describe("githubAppLink", () => {
	it("maps a repo URL", () => {
		expect(githubAppLink("https://github.com/AgentWrapper/agent-orchestrator")).toBe(
			"https://github.com/AgentWrapper/agent-orchestrator",
		);
	});

	it("maps a pull request URL", () => {
		expect(githubAppLink("https://github.com/o/r/pull/184")).toBe("https://github.com/o/r/pull/184");
	});

	it("maps an issue URL", () => {
		expect(githubAppLink("https://github.com/o/r/issues/12")).toBe("https://github.com/o/r/issues/12");
	});

	it("tolerates www, http, trailing slashes and query strings", () => {
		expect(githubAppLink("http://www.github.com/o/r/")).toBe("https://github.com/o/r");
		expect(githubAppLink("https://github.com/o/r/pull/9?diff=split")).toBe("https://github.com/o/r/pull/9");
		expect(githubAppLink("  https://github.com/o/r  ")).toBe("https://github.com/o/r");
	});

	// The case that motivated the null branch: the app cannot accept a prefilled
	// issue body, so "Report a problem" has to stay in the browser.
	it("refuses the prefilled new-issue URL", () => {
		expect(githubAppLink("https://github.com/AgentWrapper/agent-orchestrator/issues/new?body=hello")).toBeNull();
		expect(githubAppLink("https://github.com/o/r/issues/new")).toBeNull();
	});

	it("refuses non-github hosts", () => {
		expect(githubAppLink("https://gitlab.com/o/r")).toBeNull();
		expect(githubAppLink("https://example.com/github.com/o/r")).toBeNull();
		// Lookalike host — must not match on a suffix.
		expect(githubAppLink("https://notgithub.com/o/r")).toBeNull();
	});

	it("refuses paths with no repo", () => {
		expect(githubAppLink("https://github.com/")).toBeNull();
		expect(githubAppLink("https://github.com/onlyowner")).toBeNull();
	});

	it("refuses reserved roots that look like owners", () => {
		expect(githubAppLink("https://github.com/settings/profile")).toBeNull();
		expect(githubAppLink("https://github.com/orgs/acme")).toBeNull();
	});

	it("refuses deep paths the app has no stable screen for", () => {
		expect(githubAppLink("https://github.com/o/r/blob/main/README.md")).toBeNull();
		expect(githubAppLink("https://github.com/o/r/pull/184/files")).toBeNull();
		expect(githubAppLink("https://github.com/o/r/pull/abc")).toBeNull();
	});
});
