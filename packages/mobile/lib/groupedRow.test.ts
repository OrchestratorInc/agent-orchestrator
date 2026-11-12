import { describe, expect, it } from "vitest";
import { groupedRowCorners, groupedRowPosition } from "./groupedRow";

describe("groupedRowPosition", () => {
	it("places a row by its index in the section", () => {
		expect(groupedRowPosition(0, 1)).toBe("only");
		expect(groupedRowPosition(0, 3)).toBe("leading");
		expect(groupedRowPosition(1, 3)).toBe("middle");
		expect(groupedRowPosition(2, 3)).toBe("trailing");
	});
});

describe("groupedRowCorners", () => {
	const corner = { outer: 20, inner: 4, gap: 2 };
	it("rounds a section's ends fully and its joins slightly", () => {
		expect(groupedRowCorners("only", corner)).toEqual({ borderTopLeftRadius: 20, borderTopRightRadius: 20, borderBottomLeftRadius: 20, borderBottomRightRadius: 20 });
		expect(groupedRowCorners("leading", corner)).toEqual({ borderTopLeftRadius: 20, borderTopRightRadius: 20, borderBottomLeftRadius: 4, borderBottomRightRadius: 4 });
		expect(groupedRowCorners("middle", corner)).toEqual({ borderTopLeftRadius: 4, borderTopRightRadius: 4, borderBottomLeftRadius: 4, borderBottomRightRadius: 4 });
		expect(groupedRowCorners("trailing", corner)).toEqual({ borderTopLeftRadius: 4, borderTopRightRadius: 4, borderBottomLeftRadius: 20, borderBottomRightRadius: 20 });
	});
});
