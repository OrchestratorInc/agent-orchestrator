import { describe, expect, it } from "vitest";
import { labelInlineImages, proseBesideImages, removeInlineImagePath, splitInlineImagePaths } from "./messageAttachments";

const a = ".ao/attachments/attachment-a.png";
const b = ".ao/attachments/attachment-b.png";

describe("inline image paths", () => {
	it("leaves a staged path inside a longer path as text", () => {
		expect(splitInlineImagePaths(`/wt/${a} and ${a}`)).toEqual([{ text: `/wt/${a} and ` }, { path: a }]);
	});

	it("sends no prose for a message that is only its images", () => {
		expect(proseBesideImages(`${a} ${b}`, [a, b])).toBe("");
	});

	// Only the editor knows which paths are chips; a typed path is the user's text.
	it("sends a typed path untouched, attached or not", () => {
		expect(proseBesideImages(`No, look at ${b} again`, [a])).toBe(`No, look at ${b} again`);
		expect(proseBesideImages(`see\n${b}\nthanks`, [])).toBe(`see\n${b}\nthanks`);
		expect(proseBesideImages(b, [])).toBe(b);
	});

	it("labels attached inline images for one-line surfaces and keeps the reference block", () => {
		const block = `\n\nAttached files (read these files in the workspace):\n- ${a}\n- ${b}`;
		expect(labelInlineImages(`make ${b} like ${a}${block}`)).toBe(`make [Image 2] like [Image 1]${block}`);
	});
});

describe("removeInlineImagePath", () => {
	it("drops the image with one following space, as removing its chip does", () => {
		expect(removeInlineImagePath(`compare ${a} with ${b} `, a)).toBe(`compare with ${b} `);
		expect(removeInlineImagePath(`compare ${a} `, a)).toBe("compare ");
	});

	it("drops every occurrence, on every line, and leaves line breaks and other images", () => {
		expect(removeInlineImagePath(`${a} first\nthen ${a}\n${b} kept`, a)).toBe("first\nthen \n" + `${b} kept`);
	});

	it("leaves text without that image untouched, including a longer path containing it", () => {
		expect(removeInlineImagePath(`see /wt/${a} and ${b}`, a)).toBe(`see /wt/${a} and ${b}`);
		expect(removeInlineImagePath("plain words", a)).toBe("plain words");
	});
});
