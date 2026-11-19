import { describe, expect, it } from "vitest";
import { fileRoute } from "../files/navigation";
import { chatFileReference, isAbsoluteFileReference } from "./fileLinks";

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
});
