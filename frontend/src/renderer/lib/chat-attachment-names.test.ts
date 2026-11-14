import { afterEach, describe, expect, it } from "vitest";
import { readAttachmentNames, rememberAttachmentName } from "./chat-attachment-names";

const STORAGE_KEY = "ao.chatAttachmentNames.v1";

describe("chat attachment names", () => {
	afterEach(() => {
		window.localStorage.clear();
	});

	it("remembers the name a file was attached under, by its staged path", () => {
		rememberAttachmentName(".ao/attachments/attachment-a.html", "page.html");
		expect(readAttachmentNames()[".ao/attachments/attachment-a.html"]).toBe("page.html");
	});

	it("keeps only the most recent 500 files", () => {
		for (let index = 0; index < 501; index += 1) rememberAttachmentName(`.ao/attachments/attachment-${index}.txt`, `${index}.txt`);
		const names = readAttachmentNames();
		expect(Object.keys(names)).toHaveLength(500);
		expect(names[".ao/attachments/attachment-0.txt"]).toBeUndefined();
		expect(names[".ao/attachments/attachment-500.txt"]).toBe("500.txt");
	});

	it("reads changes to storage, and ignores anything that isn't a name", () => {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify({ a: "a.pdf", b: { name: "b.pdf" }, c: 3 }));
		expect(readAttachmentNames()).toEqual({ a: "a.pdf" });
		window.localStorage.setItem(STORAGE_KEY, "not json");
		expect(readAttachmentNames()).toEqual({});
	});
});
