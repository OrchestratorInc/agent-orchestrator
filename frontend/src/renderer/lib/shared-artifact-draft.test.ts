import { afterEach, describe, expect, it } from "vitest";
import { newChatDraftScope, readChatSessionDraft, writeChatComposerText } from "./chat-drafts";
import { dropOrphanSharedArtifactPrompt, dropSharedArtifactDraft, rememberSharedArtifactPrompt, withoutPrompt } from "./shared-artifact-draft";
import { useSharedArtifactDraftStore, type PendingSharedArtifact } from "../stores/shared-artifact-draft-store";

const prompt = "Work from this shared artifact, copied into your artifact folder: shared/r/r.html";
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
	localStorage.clear();
});

describe("shared artifact drafts", () => {
	it("takes the prompt out of the draft when the copy is dropped", () => {
		useSharedArtifactDraftStore.getState().setPending(pending);
		writeChatComposerText(newChatDraftScope(), `${prompt}\n\nsummarise it`);
		dropSharedArtifactDraft();
		expect(useSharedArtifactDraftStore.getState().pending).toBeNull();
		expect(draftText()).toBe("summarise it");
	});

	it("removes only the exact prompt whose copy is gone, as after a reload", () => {
		rememberSharedArtifactPrompt(prompt);
		// The person typed on the prompt's own line, and below it.
		writeChatComposerText(newChatDraftScope(), `${prompt} and compare it with last week\n\nsummarise it`);
		dropOrphanSharedArtifactPrompt();
		expect(draftText()).toBe("and compare it with last week\n\nsummarise it");
	});

	it("keeps the prompt while its copy is waiting", () => {
		useSharedArtifactDraftStore.getState().setPending(pending);
		rememberSharedArtifactPrompt(prompt);
		writeChatComposerText(newChatDraftScope(), prompt);
		dropOrphanSharedArtifactPrompt();
		expect(draftText()).toBe(prompt);
	});

	it("leaves a draft alone when no prompt was written", () => {
		writeChatComposerText(newChatDraftScope(), "Work from this shared artifact, copied into your artifact folder: shared/x/y.md is my own line");
		dropOrphanSharedArtifactPrompt();
		expect(draftText()).toContain("is my own line");
	});

	it("removes the prompt wherever it is", () => {
		expect(withoutPrompt(`note\n${prompt}`, prompt)).toBe("note");
	});
});
