import { describe, expect, it } from "vitest";
import { composerTrayItems, forgetComposerAttachments, readComposerAttachments, removeComposerAttachment, updateComposerAttachments, type ComposerAttachment } from "./composerAttachments";

const photo = (id: string): ComposerAttachment => ({ id, kind: "image", name: `${id}.jpg`, bytes: 3, image: { mimeType: "image/jpeg", data: "/9j/" } });

describe("composer attachments", () => {
	it("keeps each session's attachments apart and updates from the stored value", () => {
		updateComposerAttachments("mac.a", (current) => ({ items: [...current.items, photo("one")] }));
		updateComposerAttachments("mac.a", (current) => ({ items: [...current.items, photo("two")] }));
		updateComposerAttachments("mac.b", () => ({ items: [], notice: "Each image must be under 10 MB." }));

		expect(readComposerAttachments("mac.a").items.map((item) => item.id)).toEqual(["one", "two"]);
		expect(readComposerAttachments("mac.b")).toEqual({ items: [], notice: "Each image must be under 10 MB." });
		expect(readComposerAttachments("other-mac.a").items).toEqual([]);

		updateComposerAttachments("mac.a", () => ({ items: [] }));
		updateComposerAttachments("mac.b", () => ({ items: [] }));
	});

	it("returns one stable empty value once a session has nothing left", () => {
		updateComposerAttachments("mac.c", () => ({ items: [photo("one")] }));
		updateComposerAttachments("mac.c", () => ({ items: [] }));
		expect(readComposerAttachments("mac.c")).toBe(readComposerAttachments("never-used"));
	});

	it("forgets only the attachments a send delivered", () => {
		updateComposerAttachments("mac.d", () => ({ items: [photo("sent"), photo("added-later")], notice: "kept" }));
		forgetComposerAttachments("mac.d", new Set(["sent"]));
		expect(readComposerAttachments("mac.d")).toEqual({ items: [photo("added-later")], notice: "kept" });
		updateComposerAttachments("mac.d", () => ({ items: [] }));
	});

	it("removes the one attachment the user took out and keeps the notice", () => {
		updateComposerAttachments("mac.e", () => ({ items: [photo("keep"), photo("drop")], notice: "kept" }));
		removeComposerAttachment("mac.e", "drop");
		expect(readComposerAttachments("mac.e")).toEqual({ items: [photo("keep")], notice: "kept" });
		updateComposerAttachments("mac.e", () => ({ items: [] }));
	});

	it("shows photos from their own bytes and files by name in the tray", () => {
		const notes: ComposerAttachment = { id: "notes", kind: "resource", name: "notes.md", bytes: 2, resource: { uri: "mobile-attachment://notes.md", name: "notes.md", mimeType: "text/markdown", text: "hi" } };
		expect(composerTrayItems([photo("one"), notes])).toEqual([
			{ key: "one", name: "one.jpg", previewUri: "data:image/jpeg;base64,/9j/" },
			{ key: "notes", name: "notes.md" },
		]);
	});
});
