import { describe, expect, it } from "vitest";
import { artifactLinkForPath, artifactLinkForTarget, findPlainReferences, findSessionLinks, hasArtifactDirectorySegment, isAoLink, parseArtifactLink, parseSessionLink, resolveSessionLink, sessionArtifactPath } from "./session-links";

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

	it("searches every workspace sharing the project id before giving up", () => {
		// Remote hosts merge into one list and each contributes its own
		// standalone entry, so several workspaces can carry the same id.
		const workspaces = [
			{ id: "p", sessions: [{ id: "other" }] },
			{ id: "p", sessions: [{ id: "s", status: "working" }] },
		];
		expect(resolveSessionLink("ao://sessions/p/s", workspaces)).toEqual({ projectId: "p", sessionId: "s", isTerminated: false });
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

	it("links a path an artifact had before a rename to where it is now", () => {
		const workspaces = [{ id: "p", sessions: [{ id: "w", artifactFiles: [{ path: "final/plan.md", previousPaths: ["plan.md"] }] }] }];
		expect(artifactLinkForPath("w/plan.md", workspaces)).toBe("ao://sessions/p/w/artifacts/final/plan.md");
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

	it("links a listed artifact's path written with a line, a query, percent escapes or backslashes", () => {
		const workspaces = [{ id: "p", sessions: [{ id: "w", artifactFiles: [{ path: "plan.md" }, { path: "my plan.md" }, { path: "50%25-done.md" }] }] }];
		expect(artifactLinkForPath("/tmp/ao/artifacts/w/plan.md#L10", workspaces)).toBe("ao://sessions/p/w/artifacts/plan.md");
		expect(artifactLinkForPath("/tmp/ao/artifacts/w/plan.md?raw", workspaces)).toBe("ao://sessions/p/w/artifacts/plan.md");
		expect(artifactLinkForPath("C:%5Cao%5Cartifacts%5Cw%5Cplan.md", workspaces)).toBe("ao://sessions/p/w/artifacts/plan.md");
		// Markdown percent-encodes a link's target; the session lists the name as written.
		expect(artifactLinkForPath("/tmp/ao/artifacts/w/my%20plan.md", workspaces)).toBe("ao://sessions/p/w/artifacts/my%20plan.md");
		expect(artifactLinkForPath("w/my%20plan.md", workspaces)).toBe("ao://sessions/p/w/artifacts/my%20plan.md");
		// A name with a literal escape matches as written, before decoding.
		expect(artifactLinkForPath("/tmp/ao/artifacts/w/50%25-done.md", workspaces)).toBe("ao://sessions/p/w/artifacts/50%2525-done.md");
		expect(artifactLinkForPath("/tmp/ao/artifacts/w/plan.md%", workspaces)).toBeUndefined();
	});

	it("tells which file references have an artifact directory segment", () => {
		expect(hasArtifactDirectorySegment("/workspace/.ao/worker/artifacts/s/plan.md")).toBe(true);
		expect(hasArtifactDirectorySegment("C:\\ao\\artifacts\\s\\plan.md")).toBe(true);
		expect(hasArtifactDirectorySegment("C:%5Cao%5Cartifacts%5Cs%5Cplan.md")).toBe(true);
		expect(hasArtifactDirectorySegment("/workspace/src/plan.md")).toBe(false);
		expect(hasArtifactDirectorySegment("/workspace/src/plan.md?from=/artifacts/x")).toBe(false);
	});

	it("reads text naming an artifact as a file path, and a link target as a link", () => {
		const workspaces = [{ id: "p", sessions: [{ id: "w", artifactFiles: [{ path: "issue#42.md" }, { path: "plan.md" }] }] }];
		// Text (inline code, a report's artifact line): "#" is part of the name.
		expect(artifactLinkForPath("w/issue#42.md", workspaces)).toBe("ao://sessions/p/w/artifacts/issue%2342.md");
		expect(artifactLinkForPath("w/plan.md#L3", workspaces)).toBe("ao://sessions/p/w/artifacts/plan.md");
		expect(artifactLinkForPath("w/plan.md#L3-L7", workspaces)).toBe("ao://sessions/p/w/artifacts/plan.md");
		// An unlisted name with "#" or "?" in it is its own file, not the listed one before it.
		expect(artifactLinkForPath("w/plan.md#draft", workspaces)).toBeUndefined();
		expect(artifactLinkForPath("/tmp/ao/artifacts/w/plan.md?v2", workspaces)).toBeUndefined();
		// A link target: "#" starts its fragment, which never names the file.
		expect(artifactLinkForTarget("/tmp/ao/artifacts/w/issue%2342.md", workspaces)).toBe("ao://sessions/p/w/artifacts/issue%2342.md");
		expect(artifactLinkForTarget("/tmp/ao/artifacts/w/issue#42.md", workspaces)).toBeUndefined();
		expect(artifactLinkForTarget("/repo/src/notes.md?from=/artifacts/w/plan.md", workspaces)).toBeUndefined();
	});

	it("names an artifact whose file name holds a # or ? by its whole name", () => {
		// A file opener gets file paths (a link's own query and fragment are cut
		// before its escapes are decoded), and "#" and "?" are file name characters.
		expect(sessionArtifactPath("/workspace/.ao/worker/artifacts/session-1/issue #42?.md", "session-1")).toBe("issue #42?.md");
	});

	it("names a file in one session's artifact directory by its place there", () => {
		expect(sessionArtifactPath("/workspace/.ao/worker/artifacts/session-1/notes/Migration Guide.md", "session-1")).toBe("notes/Migration Guide.md");
		expect(sessionArtifactPath("/home/coder/.ao/worker/artifacts/session-1/plan.md", "session-1")).toBe("plan.md");
		expect(sessionArtifactPath("C:\\ao\\artifacts\\session-1\\report.html", "session-1")).toBe("report.html");
		expect(sessionArtifactPath("/workspace/.ao/worker/artifacts/session-2/plan.md", "session-1")).toBeUndefined();
		expect(sessionArtifactPath("/workspace/repository/artifacts/plan.md", "session-1")).toBeUndefined();
		expect(sessionArtifactPath("/workspace/.ao/worker/artifacts/session-1/../../repository/.env", "session-1")).toBeUndefined();
		expect(sessionArtifactPath("/workspace/.ao/worker/artifacts/session-1/", "session-1")).toBeUndefined();
		expect(sessionArtifactPath("/workspace/.ao/worker/artifacts//plan.md", "")).toBeUndefined();
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
