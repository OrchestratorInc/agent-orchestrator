import { describe, expect, it } from "vitest";
import { boardTitle } from "./board-tabs";

describe("boardTitle", () => {
	// The board is the Workers page alone; Projects moved into the sidebar.
	it.each([undefined, "(workers)", "(projects)"])("names the board Workers for %s", (route) => {
		expect(boardTitle(route)).toBe("Workers");
	});
});
