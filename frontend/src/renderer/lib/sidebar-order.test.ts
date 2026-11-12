import { describe, expect, it } from "vitest";
import { pinnedFirst } from "./sidebar-order";

describe("pinnedFirst", () => {
	it("moves pinned items to the front and keeps each group's order", () => {
		expect(pinnedFirst(["a", "b", "c", "d"], (id) => id, ["d", "b"])).toEqual(["b", "d", "a", "c"]);
	});

	it("ignores pins for items that are not listed", () => {
		expect(pinnedFirst(["a", "b"], (id) => id, ["gone"])).toEqual(["a", "b"]);
	});
});
