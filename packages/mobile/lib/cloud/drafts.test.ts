import { beforeEach, expect, it, vi } from "vitest";

const storage = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getAllKeys: async () => [...storage.keys()],
	multiRemove: async (keys: string[]) => { for (const key of keys) storage.delete(key); },
} }));

const { readComposerAttachments, updateComposerAttachments } = await import("../chat/composerAttachments");
const { forgetCloudDrafts } = await import("./drafts");

const image = { id: "a", kind: "resource" as const, name: "notes.md", bytes: 1, resource: { uri: "file:///notes.md", mimeType: "text/markdown", name: "notes.md" } };

beforeEach(() => storage.clear());

// Another account opening the same session on this phone must not find the
// last one's unsent text or files.
it("forgets Orchestrator Cloud's unsent drafts and attachments, and no machine's", async () => {
	storage.set("ao.chat.draft.orchestrator-cloud.cloud.s1", "half a reply");
	storage.set("ao.chat.draft.mac.s1", "a machine's draft");
	storage.set("ao.other", "kept");
	updateComposerAttachments("orchestrator-cloud.cloud.s1", () => ({ items: [image] }));
	updateComposerAttachments("mac.s1", () => ({ items: [image] }));

	await forgetCloudDrafts();

	expect([...storage.keys()].sort()).toEqual(["ao.chat.draft.mac.s1", "ao.other"]);
	expect(readComposerAttachments("orchestrator-cloud.cloud.s1").items).toEqual([]);
	expect(readComposerAttachments("mac.s1").items).toHaveLength(1);
});
