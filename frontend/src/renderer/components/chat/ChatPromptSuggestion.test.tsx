import { $getRoot, type LexicalEditor } from "lexical";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
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

async function clearDraft(field: HTMLElement) {
	await act(async () => {
		const editor = (field as HTMLElement & { __lexicalEditor: LexicalEditor }).__lexicalEditor;
		editor.update(() => {
			$getRoot().select(0, $getRoot().getChildrenSize()).removeText();
		}, { discrete: true });
	});
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

	it("restores the same suggestion whenever an unsent draft is cleared", async () => {
		const onDismiss = vi.fn();
		const sessionId = "prompt-suggestion-cleared";
		const { field, onSend } = renderComposer({ draftSessionId: sessionId, promptSuggestion: { ...suggestion, onDismiss } });
		for (const text of ["My own message", "Another draft"]) {
			await typeInLexicalEditor(field, text);
			expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
			expect(field).not.toHaveAttribute("aria-describedby");
			await clearDraft(field);
			expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent(suggestion.text);
			expect(readChatSessionDraft(sessionId).composer.text).toBe("");
			expect(onDismiss).not.toHaveBeenCalled();
		}
		expect(onSend).not.toHaveBeenCalled();
		await userEvent.keyboard("{Tab}");
		expect(lexicalEditorText(field)).toBe(suggestion.text);
		expect(onDismiss).not.toHaveBeenCalled();
	});

	it("restores a Tab-accepted suggestion after deleting its text until Escape dismisses it", async () => {
		const onDismiss = vi.fn();
		const sessionId = "prompt-suggestion-cleared-after-tab";
		const { field, onSend } = renderComposer({ draftSessionId: sessionId, promptSuggestion: { ...suggestion, onDismiss } });
		for (let attempt = 0; attempt < 2; attempt++) {
			await userEvent.keyboard("{Tab}");
			expect(lexicalEditorText(field)).toBe(suggestion.text);
			expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
			await clearDraft(field);
			expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent(suggestion.text);
			expect(readChatSessionDraft(sessionId).composer.text).toBe("");
			expect(onDismiss).not.toHaveBeenCalled();
		}
		fireEvent.keyDown(field, { key: "Escape" });
		expect(onDismiss).toHaveBeenCalledOnce();
		await typeInLexicalEditor(field, "Another draft");
		await clearDraft(field);
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
		expect(onSend).not.toHaveBeenCalled();
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

	it("keeps existing skill completion while typing hides the suggestion", async () => {
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
		await clearDraft(field);
		expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent(suggestion.text);
		fireEvent.keyDown(field, { key: "Escape" });
		await typeInLexicalEditor(field, "Another draft");
		await clearDraft(field);
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
	});

	it("restores the suggestion when Tab-accepted text is undone", async () => {
		const { field } = renderComposer();
		await userEvent.keyboard("{Tab}");
		await userEvent.keyboard("{Control>}z{/Control}");
		expect(lexicalEditorText(field)).toBe("");
		expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent(suggestion.text);
	});
});

describe("draft optimization", () => {
	const polished = "Fix the login bug.\n\nAcceptance: the auth tests pass.";

	function renderOptimizer(props: Partial<Parameters<typeof ChatComposer>[0]> = {}) {
		const onSend = vi.fn();
		const onOptimizeDraft = vi.fn(async () => polished);
		const view = render(
			<ChatComposer onSend={onSend} onOptimizeDraft={onOptimizeDraft} {...props} />,
			{ wrapper: TooltipProvider },
		);
		return { ...view, onSend, onOptimizeDraft, field: screen.getByRole("combobox") };
	}

	it("offers the polish as a proposal without touching the draft", async () => {
		const { field, onOptimizeDraft, onSend } = renderOptimizer();
		await typeInLexicalEditor(field, "fix login bug");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.polishAction") }));
		await waitFor(() => expect(screen.getByTestId("optimized-draft").textContent).toBe(polished));
		expect(onOptimizeDraft).toHaveBeenCalledWith("fix login bug");
		// The user's words are still the draft: an offer, not an edit.
		expect(lexicalEditorText(field)).toBe("fix login bug");
		expect(onSend).not.toHaveBeenCalled();
	});

	it("replaces the draft only after the proposal is accepted", async () => {
		const { field } = renderOptimizer();
		await typeInLexicalEditor(field, "fix login bug");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.polishAction") }));
		await waitFor(() => expect(screen.getByTestId("optimized-draft")).toBeInTheDocument());
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.useDraft") }));
		expect(lexicalEditorText(field)).toBe(polished);
		expect(screen.queryByTestId("optimized-draft")).not.toBeInTheDocument();
	});

	it("accepts the proposal with Tab and cancels it with Escape", async () => {
		const { field } = renderOptimizer();
		await typeInLexicalEditor(field, "fix login bug");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.polishAction") }));
		await waitFor(() => expect(screen.getByTestId("optimized-draft")).toBeInTheDocument());
		await userEvent.keyboard("{Tab}");
		expect(lexicalEditorText(field)).toBe(polished);

		await typeInLexicalEditor(field, "!");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.polishAction") }));
		await waitFor(() => expect(screen.getByTestId("optimized-draft")).toBeInTheDocument());
		fireEvent.keyDown(field, { key: "Escape" });
		expect(screen.queryByTestId("optimized-draft")).not.toBeInTheDocument();
		expect(lexicalEditorText(field)).toBe(`${polished}!`);
	});

	it("drops the proposal when the draft is edited, so Tab cannot replace newer words", async () => {
		const { field } = renderOptimizer();
		await typeInLexicalEditor(field, "fix login bug");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.polishAction") }));
		await waitFor(() => expect(screen.getByTestId("optimized-draft")).toBeInTheDocument());
		await typeInLexicalEditor(field, " now");
		expect(screen.queryByTestId("optimized-draft")).not.toBeInTheDocument();
		await userEvent.keyboard("{Tab}");
		expect(lexicalEditorText(field)).toBe("fix login bug now");
	});

	it("keeps the draft and reports a retryable failure when the request fails", async () => {
		const onOptimizeDraft = vi.fn(async () => undefined);
		render(
			<ChatComposer onSend={vi.fn()} onOptimizeDraft={onOptimizeDraft} optimizeFailure="unavailable" />,
			{ wrapper: TooltipProvider },
		);
		const field = screen.getByRole("combobox");
		await typeInLexicalEditor(field, "fix login bug");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.polishAction") }));
		await waitFor(() => expect(onOptimizeDraft).toHaveBeenCalled());
		expect(lexicalEditorText(field)).toBe("fix login bug");
		expect(screen.getByRole("status")).toHaveTextContent(/Your draft is unchanged/);
	});

	it("disables the control while a request is pending or the composer cannot send", () => {
		// An empty composer advertises the next-message action, which is what the
		// label says; the disable rule is the same for both actions.
		const pending = renderOptimizer({ optimizing: true });
		expect(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.suggestAction") })).toBeDisabled();
		pending.unmount();
		renderOptimizer({ disabled: true });
		expect(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.suggestAction") })).toBeDisabled();
	});

	it("drops an in-flight polish when the session switches under it", async () => {
		// Two sessions can hold the same words, so text alone cannot prove the
		// answer still belongs to the draft on screen.
		const sessionA = "optimize-scope-switch-a";
		const sessionB = "optimize-scope-switch-b";
		writeChatComposerText(sessionB, "fix login bug");
		const pendingPolish = Promise.withResolvers<string>();
		const onOptimizeDraft = vi.fn(() => pendingPolish.promise);
		const view = render(
			<ChatComposer onSend={vi.fn()} onOptimizeDraft={onOptimizeDraft} draftSessionId={sessionA} />,
			{ wrapper: TooltipProvider },
		);
		const field = screen.getByRole("combobox");
		await typeInLexicalEditor(field, "fix login bug");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.polishAction") }));
		expect(onOptimizeDraft).toHaveBeenCalledWith("fix login bug");

		view.rerender(
			<ChatComposer onSend={vi.fn()} onOptimizeDraft={onOptimizeDraft} draftSessionId={sessionB} />,
		);
		await act(async () => {
			pendingPolish.resolve(polished);
		});
		expect(screen.queryByTestId("optimized-draft")).not.toBeInTheDocument();
		expect(lexicalEditorText(field)).toBe("fix login bug");
	});

	it("asks for a next-message suggestion from an empty composer", async () => {
		const onRefreshPromptSuggestion = vi.fn();
		const onOptimizeDraft = vi.fn(async () => polished);
		render(
			<ChatComposer onSend={vi.fn()} onOptimizeDraft={onOptimizeDraft} onRefreshPromptSuggestion={onRefreshPromptSuggestion} />,
			{ wrapper: TooltipProvider },
		);
		const field = screen.getByRole("combobox");
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.promptSuggestion.suggestAction") }));
		expect(onRefreshPromptSuggestion).toHaveBeenCalledOnce();
		expect(onOptimizeDraft).not.toHaveBeenCalled();
		expect(lexicalEditorText(field)).toBe("");
	});
});

describe("slash command catalog states", () => {
	it("reports an agent that really has no commands", async () => {
		const { field } = renderComposer({ skills: [], skillsLoaded: true });
		await typeInLexicalEditor(field, "/");
		expect(await screen.findByTestId("slash-catalog-status")).toHaveTextContent(appI18n.t("chat.slashCatalog.empty"));
	});

	it("says it is still loading rather than claiming there are none", async () => {
		const { field } = renderComposer({ skills: [], skillsLoaded: false });
		await typeInLexicalEditor(field, "/");
		expect(await screen.findByTestId("slash-catalog-status")).toHaveTextContent(appI18n.t("chat.slashCatalog.loading"));
	});

	it("shows a failure with a retry instead of an empty catalog", async () => {
		const onRetrySkills = vi.fn();
		const { field } = renderComposer({ skills: [], skillsLoaded: true, skillsError: "controller not ready", onRetrySkills });
		await typeInLexicalEditor(field, "/");
		expect(await screen.findByTestId("slash-catalog-status")).toHaveTextContent(appI18n.t("chat.slashCatalog.errorDetail", { message: "controller not ready" }));
		await userEvent.click(screen.getByRole("button", { name: appI18n.t("chat.slashCatalog.retry") }));
		expect(onRetrySkills).toHaveBeenCalledOnce();
	});

	it("keeps the ordinary menu, and never invents /goal, when commands exist", async () => {
		const { field } = renderComposer({
			skills: [{ name: "review", displayName: "review", description: "Review changes", source: "user" }],
			skillsLoaded: true,
		});
		await typeInLexicalEditor(field, "/");
		const menu = await screen.findByRole("listbox");
		expect(menu).toHaveTextContent("review");
		expect(screen.queryByTestId("slash-catalog-status")).not.toBeInTheDocument();
		expect(menu).not.toHaveTextContent("/goal");
	});
});
