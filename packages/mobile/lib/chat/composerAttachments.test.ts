import { describe, expect, it } from "vitest";
import { forgetComposerAttachments, readComposerAttachments, updateComposerAttachments, type ComposerAttachment } from "./composerAttachments";

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
});
