import { describe, expect, it } from "vitest";
import { summaryPreview } from "./ReviewComments";

describe("summaryPreview", () => {
	it("skips a heading that only restates the verdict", () => {
		expect(summaryPreview(["Changes requested", "The saved-filters feature works for the happy path."], ["Changes requested"]))
			.toBe("The saved-filters feature works for the happy path.");
	});

	it("drops a verdict lead-in and capitalizes what follows", () => {
		expect(summaryPreview(["Changes requested: the generator now throws after yielding."], ["Changes requested"]))
			.toBe("The generator now throws after yielding.");
		expect(summaryPreview(["Approved. The validation covers empty names."], ["Approved"])).toBe("The validation covers empty names.");
	});

	it("matches the English label when the UI is in another language", () => {
		expect(summaryPreview(["Approved — ship it."], ["Aprobado", "Approved"])).toBe("Ship it.");
	});

	it("keeps a line that only starts with the same word", () => {
		expect(summaryPreview(["Approvedness is not a word."], ["Approved"])).toBe("Approvedness is not a word.");
		expect(summaryPreview(["Looks good."], ["Approved"])).toBe("Looks good.");
	});

	it("keeps a lone verdict line when nothing follows it", () => {
		expect(summaryPreview(["Approved."], ["Approved"])).toBe("Approved.");
	});

	it("leaves comments without a verdict alone", () => {
		expect(summaryPreview(["Changes requested here?"], [])).toBe("Changes requested here?");
		expect(summaryPreview([], ["Approved"])).toBe("");
	});
});
