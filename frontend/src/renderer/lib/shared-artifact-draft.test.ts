import { afterEach, describe, expect, it } from "vitest";
import { newChatDraftScope, readChatSessionDraft, writeChatComposerText } from "./chat-drafts";
import { dropOrphanSharedArtifactPrompt, dropSharedArtifactDraft, withoutPrompt } from "./shared-artifact-draft";
import { useSharedArtifactDraftStore, type PendingSharedArtifact } from "../stores/shared-artifact-draft-store";

const template = "Work from this shared artifact, copied into your artifact folder: \u0000";
const prompt = template.replace("\u0000", "shared/r/r.html");
const pending: PendingSharedArtifact = {
	key: { baseUrl: "https://cp.example", userId: "u1", linkId: "link-1" },
	name: "r.html",
	updatedAt: "d1",
	copy: { entry: "shared/r/r.html", dir: "shared/r", files: [], missing: [], fingerprint: "f1" },
	prompt,
};
const draftText = () => readChatSessionDraft(newChatDraftScope()).composer.text;

afterEach(() => {
	useSharedArtifactDraftStore.getState().clearPending();
	writeChatComposerText(newChatDraftScope(), "");
});

describe("shared artifact drafts", () => {
	it("takes the prompt out of the draft when the copy is dropped", () => {
		useSharedArtifactDraftStore.getState().setPending(pending);
		writeChatComposerText(newChatDraftScope(), `${prompt}\n\nsummarise it`);
		dropSharedArtifactDraft();
		expect(useSharedArtifactDraftStore.getState().pending).toBeNull();
		expect(draftText()).toBe("summarise it");
	});

	it("removes a prompt whose copy is gone, as after a reload", () => {
		writeChatComposerText(newChatDraftScope(), `${prompt}\n\nsummarise it`);
		dropOrphanSharedArtifactPrompt(template);
		expect(draftText()).toBe("summarise it");
	});

	it("keeps the prompt while its copy is waiting", () => {
		useSharedArtifactDraftStore.getState().setPending(pending);
		writeChatComposerText(newChatDraftScope(), prompt);
		dropOrphanSharedArtifactPrompt(template);
		expect(draftText()).toBe(prompt);
	});

	it("removes the prompt wherever it is", () => {
		expect(withoutPrompt(`note\n${prompt}`, prompt)).toBe("note");
	});
});
