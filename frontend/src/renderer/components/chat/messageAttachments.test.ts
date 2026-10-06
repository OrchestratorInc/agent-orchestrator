import { describe, expect, it } from "vitest";
import { attachedInlineImages, labelInlineImages, splitInlineImagePaths } from "./messageAttachments";

const a = ".ao/attachments/attachment-a.png";
const b = ".ao/attachments/attachment-b.png";

describe("inline image paths", () => {
	it("leaves a staged path inside a longer path as text", () => {
		expect(splitInlineImagePaths(`/wt/${a} and ${a}`)).toEqual([{ text: `/wt/${a} and ` }, { path: a }]);
	});

	it("sends only images the message attaches, dropping a chip whose image was removed", () => {
		expect(attachedInlineImages(`compare ${a} with ${b} please`, [a])).toBe(`compare ${a} with please`);
	});

	it("sends no prose for a message that is only its images", () => {
		expect(attachedInlineImages(`${a} ${b}`, [a, b])).toBe("");
	});

	it("labels attached inline images for one-line surfaces and keeps the reference block", () => {
		const block = `\n\nAttached files (read these files in the workspace):\n- ${a}\n- ${b}`;
		expect(labelInlineImages(`make ${b} like ${a}${block}`)).toBe(`make [Image 2] like [Image 1]${block}`);
	});
});
