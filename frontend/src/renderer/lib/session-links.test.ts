import { describe, expect, it } from "vitest";
import { artifactLinkForPath, findPlainReferences, findSessionLinks, isAoLink, parseArtifactLink, parseSessionLink, resolveSessionLink } from "./session-links";

describe("session links", () => {
	it.each([
		["ao://sessions/project/session", { projectId: "project", sessionId: "session" }],
		["ao://sessions/project%20one/session%2Done", { projectId: "project one", sessionId: "session-one" }],
	])("parses %s", (url, expected) => expect(parseSessionLink(url)).toEqual(expected));

	it.each([
		"AO://sessions/project/session", "a0://sessions/project/session", "ao://session/project/session",
		"ao://sessions/project", "ao://sessions/project/session/extra", "ao://sessions//session",
		"ao://sessions/project/session?kill=true", "ao://sessions/project/session#chat",
		"ao://user@sessions/project/session", "ao://sessions/project/%ZZ", "ao://sessions/project/%2Faction",
		"ao://sessions/project/session/kill", "ao://sessions/%20/%20", "ao://sessions/project/%09",
	])("rejects unsupported value %s", (url) => expect(parseSessionLink(url)).toBeUndefined());

	it("trims adjacent punctuation while preserving encoded IDs", () => {
		expect(findSessionLinks("See (ao://sessions/p/s), then ao://sessions/p/t.")).toEqual([
			{ start: 5, end: 22, text: "ao://sessions/p/s" },
			{ start: 30, end: 47, text: "ao://sessions/p/t" },
		]);
	});

	it.each([
		['"ao://sessions/proj/sess"', "ao://sessions/proj/sess"],
		["'ao://sessions/proj/sess'", "ao://sessions/proj/sess"],
		["[ao://sessions/proj/sess]", "ao://sessions/proj/sess"],
		["«ao://sessions/proj/sess»", "ao://sessions/proj/sess"],
		["<ao://sessions/proj/sess>", "ao://sessions/proj/sess"],
		["&lt;ao://sessions/proj/sess&gt;", "ao://sessions/proj/sess"],
		['"ao://sessions/proj/sess").', "ao://sessions/proj/sess"],
	])("does not include a wrapping delimiter in %s", (text, expected) => {
		expect(findSessionLinks(text)).toEqual([
			{ start: text.indexOf("ao://"), end: text.indexOf("ao://") + expected.length, text: expected },
		]);
	});

	it("resolves by stable project and session IDs with termination state", () => {
		const workspaces = [{ id: "p", sessions: [{ id: "s", title: "renamed", status: "terminated" }] }];
		expect(resolveSessionLink("ao://sessions/p/s", workspaces)).toEqual({ projectId: "p", sessionId: "s", isTerminated: true });
		expect(resolveSessionLink("ao://sessions/p/missing", workspaces)).toBeUndefined();
	});

	it.each([
		["ao://sessions/p/s/artifacts/plan.md", { projectId: "p", sessionId: "s", path: "plan.md" }],
		["ao://sessions/p/s/artifacts/plans/my%20plan.md", { projectId: "p", sessionId: "s", path: "plans/my plan.md" }],
	])("parses artifact link %s", (url, expected) => {
		expect(parseArtifactLink(url)).toEqual(expected);
		expect(parseSessionLink(url)).toBeUndefined();
		expect(isAoLink(url)).toBe(true);
	});

	it.each([
		"ao://sessions/p/s/artifacts", "ao://sessions/p/s/artifacts/", "ao://sessions/p/s/files/plan.md",
		"ao://sessions/p/s/artifacts/../secret", "ao://sessions/p/s/artifacts/a/%2E%2E/b", "ao://sessions/p/s/artifacts/a%2Fb",
		"ao://sessions/p/s/artifacts/a%5Cb", "ao://sessions/p/s/artifacts/plan.md?x=1", "ao://sessions/p/s/artifacts/plan.md#top",
		"ao://sessions/p/s/artifacts/%ZZ",
	])("rejects unsupported artifact link %s", (url) => expect(parseArtifactLink(url)).toBeUndefined());

	it("finds raw artifact links and resolves them to their session", () => {
		expect(findSessionLinks("See ao://sessions/p/s/artifacts/plan.md.")).toEqual([
			{ start: 4, end: 39, text: "ao://sessions/p/s/artifacts/plan.md" },
		]);
		const workspaces = [{ id: "p", sessions: [{ id: "s", title: "worker", status: "terminated" }] }];
		expect(resolveSessionLink("ao://sessions/p/s/artifacts/plan.md", workspaces)).toEqual({ projectId: "p", sessionId: "s", isTerminated: true });
	});

	it("links paths naming a listed artifact, by session-relative or artifact-directory path", () => {
		const workspaces = [{ id: "p 1", sessions: [{ id: "w", artifactFiles: [{ path: "plan.md" }, { path: "plans/my plan.md" }] }] }];
		expect(artifactLinkForPath("w/plan.md", workspaces)).toBe("ao://sessions/p%201/w/artifacts/plan.md");
		expect(artifactLinkForPath("w/plans/my plan.md", workspaces)).toBe("ao://sessions/p%201/w/artifacts/plans/my%20plan.md");
		expect(artifactLinkForPath(" /tmp/ao data/artifacts/w/plans/my plan.md ", workspaces)).toBe("ao://sessions/p%201/w/artifacts/plans/my%20plan.md");
		expect(artifactLinkForPath("w/plans/my  plan.md", workspaces)).toBeUndefined();
		expect(artifactLinkForPath("/tmp/ao/artifacts/w/plan.md", workspaces)).toBe("ao://sessions/p%201/w/artifacts/plan.md");
		expect(artifactLinkForPath("C:\\ao\\artifacts\\w\\plan.md", workspaces)).toBe("ao://sessions/p%201/w/artifacts/plan.md");
		expect(artifactLinkForPath("w/missing.md", workspaces)).toBeUndefined();
		expect(artifactLinkForPath("/tmp/w/plan.md", workspaces)).toBeUndefined();
		expect(artifactLinkForPath("plan.md", workspaces)).toBeUndefined();
	});

	it("finds plain paths and named sessions, without prose or wrapping punctuation", () => {
		const sessions = new Map([["proj-297", "proj"]]);
		const found = (text: string) => findPlainReferences(text, sessions).map((match) => [text.slice(match.start, match.end), match.node.type]);
		expect(found("See (/Users/me/.ao/shot.png), ~/.ao and src/a.tsx:42.")).toEqual([
			["/Users/me/.ao/shot.png", "inlineCode"], ["~/.ao", "inlineCode"], ["src/a.tsx:42", "inlineCode"],
		]);
		expect(found("frontend/src/renderer in session proj-297, not proj-2970.")).toEqual([
			["frontend/src/renderer", "inlineCode"], ["proj-297", "link"],
		]);
		expect(found("src/App.test.tsx, src/index.d.ts and config/.env.local")).toEqual([
			["src/App.test.tsx", "inlineCode"], ["src/index.d.ts", "inlineCode"], ["config/.env.local", "inlineCode"],
		]);
		expect(found("and/or CI/CD /review /tmp 1/2")).toEqual([]);
		expect(findPlainReferences("proj-297", sessions)[0]!.node.url).toBe("ao://sessions/proj/proj-297");
	});
});
