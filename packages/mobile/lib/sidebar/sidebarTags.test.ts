import { describe, expect, it } from "vitest";
import type { SidebarProjectNode, SidebarSessionRow, SidebarTree } from "./sidebarModel";
import { applyTag, canSaveTag, chosenTag, normalizedTag, tagChoice, type SidebarTag } from "./sidebarTags";

const row = (key: string) => ({ key }) as SidebarSessionRow;
const project = (key: string, sessions: string[]) => ({ key, sessions: sessions.map(row) }) as SidebarProjectNode;
const tree: SidebarTree = {
	pinned: [row("h:p1")],
	standalone: [row("h:s1"), row("h:s2")],
	projects: [project("h:web", ["h:w1", "h:w2"]), project("h:api", ["h:a1", "h:a2"]), project("h:docs", ["h:d1"])],
};
const tag = (over: Partial<SidebarTag>): SidebarTag => ({ id: "t", name: "Mine", projects: [], sessions: [], ...over });

describe("applyTag", () => {
	it("keeps a tagged project whole, with every session", () => {
		const narrowed = applyTag(tree, tag({ projects: ["h:web"] }));
		expect(narrowed.projects.map((node) => [node.key, node.sessions.map((item) => item.key)])).toEqual([["h:web", ["h:w1", "h:w2"]]]);
		expect(narrowed.standalone).toEqual([]);
	});

	it("keeps only the tagged sessions of a project that is not tagged", () => {
		const narrowed = applyTag(tree, tag({ sessions: ["h:a2", "h:s2"] }));
		expect(narrowed.projects.map((node) => [node.key, node.sessions.map((item) => item.key)])).toEqual([["h:api", ["h:a2"]]]);
		expect(narrowed.standalone.map((item) => item.key)).toEqual(["h:s2"]);
	});

	it("leaves the drawer's pinned sessions alone", () => {
		expect(applyTag(tree, tag({ projects: ["h:docs"] })).pinned).toBe(tree.pinned);
	});
});

describe("tag choice", () => {
	const tags = [tag({ id: "a" }), tag({ id: "b", name: "Other" })];
	it("names a tag in the devices bar's slot, and forgets one that is gone", () => {
		expect(chosenTag(tagChoice("b"), tags)?.name).toBe("Other");
		expect(chosenTag(tagChoice("gone"), tags)).toBeUndefined();
		expect(chosenTag("h_machine", tags)).toBeUndefined();
	});
});

describe("saving a tag", () => {
	it("needs a name and something to gather", () => {
		expect(canSaveTag({ name: " ", projects: ["h:web"], sessions: [] })).toBe(false);
		expect(canSaveTag({ name: "Mine", projects: [], sessions: [] })).toBe(false);
		expect(canSaveTag({ name: "Mine", projects: [], sessions: ["h:s1"] })).toBe(true);
	});

	it("trims the name and drops repeats", () => {
		expect(normalizedTag(tag({ name: "  Mine ", projects: ["a", "a"], sessions: ["b", "b"] }))).toEqual({ id: "t", name: "Mine", projects: ["a"], sessions: ["b"] });
	});
});
