import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { readChatSessionDraft, writeChatComposerText } from "../../lib/chat-drafts";
import { lexicalEditorText, typeAndPressInLexicalEditor, typeInLexicalEditor } from "../../test/lexical";
import { TooltipProvider } from "../ui/tooltip";
import { ChatComposer } from "./ChatComposer";

const suggestion = { id: "reply-1", text: "Fix the race and rerun the affected tests." };

function renderComposer(props: Partial<Parameters<typeof ChatComposer>[0]> = {}) {
	const onSend = vi.fn();
	const view = render(<ChatComposer onSend={onSend} promptSuggestion={suggestion} {...props} />, { wrapper: TooltipProvider });
	return { ...view, onSend, field: screen.getByRole("combobox") };
}

describe("next-message suggestion", () => {
	it("shows ghost text outside the saved draft and inserts it with Tab before sending", async () => {
		const sessionId = "prompt-suggestion-accept";
		const { field, onSend } = renderComposer({ draftSessionId: sessionId });
		expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent(suggestion.text);
		expect(field).toHaveAccessibleDescription(`Suggested next message: ${suggestion.text}. Press Tab to use it, or Escape to dismiss.`);
		expect(lexicalEditorText(field)).toBe("");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("");

		await userEvent.keyboard("{Tab}");
		expect(lexicalEditorText(field)).toBe(suggestion.text);
		expect(readChatSessionDraft(sessionId).composer.text).toBe(suggestion.text);
		expect(onSend).not.toHaveBeenCalled();
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledWith(suggestion.text, undefined, expect.any(String)));
	});

	it("does not accept or send ghost text on Enter", async () => {
		const { field, onSend } = renderComposer();
		fireEvent.keyDown(field, { key: "Enter" });
		expect(lexicalEditorText(field)).toBe("");
		expect(onSend).not.toHaveBeenCalled();
	});

	it.each(["/review", "Summarize @README.md"])("sends an accepted suggestion containing completion syntax verbatim: %s", async (text) => {
		const { field, onSend } = renderComposer({
			promptSuggestion: { id: `syntax-${text}`, text },
			skills: [{ name: "review", displayName: "review", description: "Review changes", source: "user" }],
			filePaths: ["README.md"],
		});
		await userEvent.keyboard("{Tab}");
		expect(lexicalEditorText(field)).toBe(`${text} `);
		expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledWith(text));
	});

	it("restores ordinary file completion when the user edits an accepted suggestion", async () => {
		const { field } = renderComposer({ promptSuggestion: { id: "editable-file", text: "Summarize @READ" }, filePaths: ["README.md"] });
		await userEvent.keyboard("{Tab}");
		expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
		await typeInLexicalEditor(field, "and @READM");
		expect(screen.getByRole("listbox")).toBeInTheDocument();
		await userEvent.keyboard("{Tab}");
		expect(lexicalEditorText(field)).toBe("Summarize @READ and README.md ");
	});

	it("sends an accepted file-reference suggestion after restoring its saved draft", async () => {
		const text = "Summarize @README.md";
		const props = { draftSessionId: "prompt-suggestion-restored-reference", promptSuggestion: { id: "restored-file", text }, filePaths: ["README.md"] };
		const first = renderComposer(props);
		await userEvent.keyboard("{Tab}");
		expect(readChatSessionDraft(props.draftSessionId).composer.text).toBe(`${text} `);
		first.unmount();
		const { field, onSend } = renderComposer(props);
		await waitFor(() => expect(lexicalEditorText(field)).toBe(`${text} `));
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledWith(text, undefined, expect.any(String)));
	});

	it("notifies the owning suggestion cache when Escape dismisses it", () => {
		const onDismiss = vi.fn();
		const { field } = renderComposer({ promptSuggestion: { ...suggestion, onDismiss } });
		fireEvent.keyDown(field, { key: "Escape" });
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	it("dismisses with Escape until the next reply", async () => {
		const { field, onSend, rerender } = renderComposer();
		fireEvent.keyDown(field, { key: "Escape" });
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
		fireEvent.keyDown(field, { key: "Tab" });
		expect(lexicalEditorText(field)).toBe("");
		rerender(<ChatComposer onSend={onSend} promptSuggestion={{ id: "reply-2", text: "Write the review comment." }} />);
		expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent("Write the review comment.");
	});

	it("preserves fast typing before Tab and never overwrites the draft", async () => {
		const { field } = renderComposer();
		await typeAndPressInLexicalEditor(field, "My own message", "Tab");
		expect(lexicalEditorText(field)).toBe("My own message");
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
	});

	it("keeps existing skill completion after typing dismisses the suggestion", async () => {
		const { field } = renderComposer({ skills: [{ name: "review", displayName: "review", description: "Review changes", source: "user" }] });
		await typeInLexicalEditor(field, "/rev");
		await userEvent.keyboard("{Tab}");
		expect(lexicalEditorText(field)).toBe("/review ");
	});

	it.each([
		{ shiftKey: true }, { ctrlKey: true }, { metaKey: true }, { altKey: true }, { isComposing: true },
	])("does not accept a modified or composing Tab (%j)", (modifiers) => {
		const { field } = renderComposer();
		fireEvent.keyDown(field, { key: "Tab", ...modifiers });
		expect(lexicalEditorText(field)).toBe("");
	});

	it.each([
		{ disabled: true }, { willQueue: true }, { starting: true }, { editingQueuedTurnId: "queued-turn" },
	])("hides suggestions when normal message entry is unavailable (%j)", (props) => {
		renderComposer(props);
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
	});

	it("keeps a restored draft when a suggestion arrives late", async () => {
		const sessionId = "prompt-suggestion-existing-draft";
		writeChatComposerText(sessionId, "Saved message");
		const { field, onSend, rerender } = renderComposer({ draftSessionId: sessionId, promptSuggestion: undefined });
		rerender(<ChatComposer onSend={onSend} draftSessionId={sessionId} promptSuggestion={suggestion} />);
		await waitFor(() => expect(lexicalEditorText(field)).toBe("Saved message"));
		fireEvent.keyDown(field, { key: "Tab" });
		expect(lexicalEditorText(field)).toBe("Saved message");
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
	});

	it("undoes accepted suggestion text without restoring the ghost", async () => {
		const { field } = renderComposer();
		await userEvent.keyboard("{Tab}");
		await userEvent.keyboard("{Control>}z{/Control}");
		expect(lexicalEditorText(field)).toBe("");
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
	});
});
