import { describe, expect, it } from "vitest";
import { fileRoute } from "../files/navigation";
import { chatFileReference, isAbsoluteFileReference, isBareFileReference, matchAbsoluteFile, matchBareFile } from "./fileLinks";

describe("chat file links", () => {
	it("reads the workspace path and line agents cite", () => {
		expect(chatFileReference("frontend/src/main/browser-view-host.ts:1748")).toEqual({ path: "frontend/src/main/browser-view-host.ts", line: 1748 });
		expect(chatFileReference("frontend/src/renderer/hooks/useBrowserView.ts:1182:7")).toEqual({ path: "frontend/src/renderer/hooks/useBrowserView.ts", line: 1182 });
		expect(chatFileReference("lib/api.ts#L12-L20")).toEqual({ path: "lib/api.ts", line: 12 });
		expect(chatFileReference("./README.md")).toEqual({ path: "README.md" });
		expect(chatFileReference("README.md:12")).toEqual({ path: "README.md", line: 12 });
	});

	it("keeps absolute and file:// paths for the daemon to resolve", () => {
		expect(chatFileReference("/Users/me/.ao/worktrees/p/s/src/a.go:3")).toEqual({ path: "/Users/me/.ao/worktrees/p/s/src/a.go", line: 3 });
		expect(chatFileReference("file:///Users/me/src/a%20b.ts")).toEqual({ path: "/Users/me/src/a b.ts" });
	});

	it("leaves web links, anchors, other schemes and bare words alone", () => {
		expect(chatFileReference("https://github.com/o/r/blob/main/a.ts")).toBeUndefined();
		expect(chatFileReference("#usage")).toBeUndefined();
		expect(chatFileReference("mailto:someone@example.com")).toBeUndefined();
		expect(chatFileReference("ao://sessions/p/s")).toBeUndefined();
		expect(chatFileReference("//cdn.example.com/x.js")).toBeUndefined();
		expect(chatFileReference("../outside/secret.txt")).toBeUndefined();
		expect(chatFileReference("notes")).toBeUndefined();
		// URL parsing would fold the `..` away before it could be seen.
		expect(chatFileReference("file:///worktree/src/../README.md:12")).toBeUndefined();
	});

	it("takes a known extensionless file at a line, but not a host and port", () => {
		expect(chatFileReference("Dockerfile:12")).toEqual({ path: "Dockerfile", line: 12 });
		expect(chatFileReference("Makefile:8")).toEqual({ path: "Makefile", line: 8 });
		expect(chatFileReference("Dockerfile")).toBeUndefined();
		expect(chatFileReference("localhost:3000")).toBeUndefined();
		expect(chatFileReference("server:8080")).toBeUndefined();
	});

	it("tells the paths the daemon must place from workspace-relative ones", () => {
		expect(isAbsoluteFileReference("/Users/me/src/a.ts")).toBe(true);
		expect(isAbsoluteFileReference("~/src/a.ts")).toBe(true);
		expect(isAbsoluteFileReference("C:/src/a.ts")).toBe(true);
		expect(isAbsoluteFileReference("src/a.ts")).toBe(false);
	});

	it("opens a cited line on the whole file, and anything else on its diff", () => {
		expect(fileRoute("s1", "src/a.ts", "h1", undefined, 12).params).toMatchObject({ path: "src/a.ts", mode: "file", line: "12", hostId: "h1" });
		expect(fileRoute("s1", "src/a.ts", "h1").params).toMatchObject({ mode: "diff" });
		expect(fileRoute("s1", "src/a.ts", "h1").params).not.toHaveProperty("line");
	});

	it("names a bare file only when the session has exactly one by that name", () => {
		const paths = ["README.md", "lib/chat/api.ts", "lib/files/api.ts", "lib/chat/fileLinks.ts"];
		expect(isBareFileReference("README.md")).toBe(true);
		expect(isBareFileReference("lib/api.ts")).toBe(false);
		expect(isBareFileReference("/abs/api.ts")).toBe(false);
		expect(matchBareFile("README.md", paths)).toBe("README.md");
		expect(matchBareFile("fileLinks.ts", paths)).toBe("lib/chat/fileLinks.ts");
		// Two api.ts: no telling which one was meant.
		expect(matchBareFile("api.ts", paths)).toBeNull();
		expect(matchBareFile("example.com", paths)).toBeNull();
		// A list cut short may leave out a second file by the same name.
		expect(matchBareFile("README.md", paths, false)).toBe("README.md");
		expect(matchBareFile("fileLinks.ts", paths, false)).toBeNull();
	});

	it("matches an absolute path to the longest worktree path it ends with", () => {
		const found = ["src/a.ts", "pkg/src/a.ts", "other/a.ts"];
		expect(matchAbsoluteFile("/sandbox/repo/pkg/src/a.ts", found)).toBe("pkg/src/a.ts");
		expect(matchAbsoluteFile("/sandbox/repo/lib/a.ts", found)).toBeNull();
	});
});
