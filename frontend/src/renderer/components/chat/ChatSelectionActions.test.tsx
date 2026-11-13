import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { ChatSelectionActions } from "./ChatSelectionActions";
import { appI18n } from "../../i18n/instance";
import { useKeybindingsStore } from "../../stores/keybindings-store";
import { isMacPlatform } from "../../lib/platform";

vi.mock("../../hooks/useChatSelectionPosition", () => ({ useChatSelectionPosition: () => ({ left: 8, top: 8, visible: true }) }));
vi.mock("../../lib/platform", () => ({ isMacPlatform: vi.fn(() => false) }));

beforeEach(() => {
	useKeybindingsStore.setState({ overrides: {} });
	vi.mocked(isMacPlatform).mockReturnValue(false);
});
afterEach(() => {
	window.getSelection()?.removeAllRanges();
	document.querySelectorAll("[data-test-chat-selection]").forEach(element => element.remove());
});

function setup() {
	const range = document.createRange();
	const excerpt = { id: "selection", conversationId: "parent", messageId: "message", revision: 4, text: "exact selected text", role: "assistant" as const };
	return { selection: { range, excerpt }, viewportRef: { current: null }, onInvalid: vi.fn(), onAdd: vi.fn() };
}

describe("chat selection actions", () => {
	it.each([false, true])("uses live bindings and hints, including unassign and reset (Mac: %s)", async (isMac) => {
		vi.mocked(isMacPlatform).mockReturnValue(isMac);
		const props = setup();
		const viewport = document.createElement("div");
		viewport.dataset.testChatSelection = ""; viewport.textContent = "selected chat text";
		document.body.append(viewport);
		props.selection.range.selectNodeContents(viewport);
		window.getSelection()!.addRange(props.selection.range);
		render(<ChatSelectionActions {...props} viewportRef={{ current: viewport }} />);
		const button = screen.getByRole("button", { name: "Add to chat" });
		act(() => button.focus());
		window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(props.selection.range);
		const hint = () => screen.getByRole("tooltip").textContent;
		expect(hint()).toBe(isMac ? "Add to chat (⌘L)" : "Add to chat (Ctrl+L)");
		const primary = { ctrlKey: !isMac, metaKey: isMac };
		await act(async () => { expect(fireEvent.keyDown(viewport, { key: "l", ...primary })).toBe(false); });
		expect(props.onAdd).toHaveBeenCalledTimes(1);
		act(() => useKeybindingsStore.setState({ overrides: { "add-selection-to-chat": [{ key: "j", ctrl: !isMac, meta: isMac, shift: true, alt: true }] } }));
		expect(hint()).toBe(isMac ? "Add to chat (⌥⇧⌘J)" : "Add to chat (Ctrl+Alt+Shift+J)");
		expect(fireEvent.keyDown(viewport, { key: "l", ...primary })).toBe(true);
		expect(fireEvent.keyDown(viewport, { key: "j", ...primary, shiftKey: true, altKey: true, isComposing: true })).toBe(true);
		await act(async () => { expect(fireEvent.keyDown(viewport, { key: "j", ...primary, shiftKey: true, altKey: true })).toBe(false); });
		expect(props.onAdd).toHaveBeenCalledTimes(2);
		act(() => useKeybindingsStore.setState({ overrides: { "add-selection-to-chat": [] } }));
		expect(hint()).toBe("Add to chat");
		expect(fireEvent.keyDown(viewport, { key: "l", ...primary })).toBe(true);
		expect(fireEvent.keyDown(viewport, { key: "j", ...primary, shiftKey: true, altKey: true })).toBe(true);
		act(() => useKeybindingsStore.setState({ overrides: {} }));
		await act(async () => { expect(fireEvent.keyDown(viewport, { key: "l", ...primary })).toBe(false); });
		expect(props.onAdd).toHaveBeenCalledTimes(3);
	});
	it("translates excerpt errors without dismissing the selected text", async () => {
		const props = setup();
		props.onAdd.mockRejectedValue(new Error("chat.draft.excerptTextLimit"));
		const view = render(<ChatSelectionActions {...props} />);
		try {
			await act(async () => fireEvent.click(screen.getByRole("button", { name: "Add to chat" })));
			expect(screen.getByRole("alert")).toHaveTextContent("Selected text exceeds 24 KiB");
			await act(async () => { await appI18n.changeLanguage("fr"); });
			expect(screen.getByRole("alert")).toHaveTextContent("Le texte sélectionné dépasse 24 Kio");
			expect(props.onInvalid).not.toHaveBeenCalled();
		} finally {
			view.unmount();
			await appI18n.changeLanguage("en");
		}
	});
	it.each(["{Enter}", " "])("supports native keyboard activation with %s", async (key) => {
		const props = setup(); render(<ChatSelectionActions {...props} />);
		const user = userEvent.setup();
		await user.tab(); await user.tab(); await user.keyboard(key);
		expect(props.onAdd).toHaveBeenCalledExactlyOnceWith("");
	});
	it("adds the selection with the comment typed inline", async () => {
		const props = setup(); render(<ChatSelectionActions {...props} />);
		const user = userEvent.setup();
		await user.type(screen.getByRole("textbox", { name: "Comment on the selected text" }), "  make it shorter {Enter}");
		expect(props.onAdd).toHaveBeenCalledExactlyOnceWith("make it shorter");
	});
	it("edits an existing annotation's comment and removes it", async () => {
		const props = setup(); const onRemove = vi.fn();
		props.selection.excerpt = { ...props.selection.excerpt, comment: "old" } as typeof props.selection.excerpt;
		render(<ChatSelectionActions {...props} onRemove={onRemove} />);
		const box = screen.getByRole("textbox", { name: "Comment on the selected text" });
		expect(box).toHaveValue("old");
		expect(box).toHaveFocus();
		const user = userEvent.setup();
		await user.clear(box); await user.type(box, "new");
		await user.click(screen.getByRole("button", { name: "Save comment" }));
		expect(props.onAdd).toHaveBeenCalledExactlyOnceWith("new");
		await user.click(screen.getByRole("button", { name: "Remove annotation" }));
		expect(onRemove).toHaveBeenCalledTimes(1);
		expect(props.onInvalid).toHaveBeenCalledTimes(1);
		await user.type(box, "{Escape}");
		expect(props.onInvalid).toHaveBeenCalledTimes(2);
	});
	it("shows the side action disabled while checking, then enables it", () => {
		const props = setup(); const onSide = vi.fn();
		const view = render(<ChatSelectionActions {...props} checkingSide />);
		expect(screen.getByRole("button", { name: "Add to side chat" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Add to chat" })).toBeEnabled();
		view.rerender(<ChatSelectionActions {...props} onSide={onSide} />);
		expect(screen.getByRole("button", { name: "Add to side chat" })).toBeEnabled();
		view.rerender(<ChatSelectionActions {...props} />);
		expect(screen.getAllByRole("button")).toHaveLength(1);
	});
	it("locks duplicate clicks and delivers the original reference once", async () => {
		const props = setup(); let finish!: () => void;
		const onSide = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
		render(<ChatSelectionActions {...props} onSide={onSide} />);
		const button = screen.getByRole("button", { name: "Add to side chat" });
		expect(fireEvent.mouseDown(button)).toBe(false);
		fireEvent.click(button); fireEvent.click(button);
		expect(onSide).toHaveBeenCalledExactlyOnceWith(props.selection.excerpt);
		expect(screen.getByRole("button", { name: "Add to chat" })).toBeDisabled();
		await act(async () => finish());
		expect(props.onInvalid).toHaveBeenCalledTimes(1);
	});
	it("does not dismiss a newer selection after an old request settles", async () => {
		const props = setup(); let finish!: () => void;
		const onSide = () => new Promise<void>((resolve) => { finish = resolve; });
		const view = render(<ChatSelectionActions {...props} onSide={onSide} />);
		fireEvent.click(screen.getByRole("button", { name: "Add to side chat" }));
		view.unmount(); await act(async () => finish());
		expect(props.onInvalid).not.toHaveBeenCalled();
	});
	it.each([new Error("Destination unavailable"), { error: { code: "SIDE_UNAVAILABLE", message: "Destination unavailable" } }])("keeps a failed action retryable and explains its error: %j", async (failure) => {
		const props = setup(); const onSide = vi.fn().mockRejectedValue(failure);
		render(<ChatSelectionActions {...props} onSide={onSide} />);
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Add to side chat" })));
		expect(screen.getByRole("alert")).toHaveTextContent("Destination unavailable");
		expect(screen.getByRole("button", { name: "Add to side chat" })).toBeEnabled();
		expect(props.onInvalid).not.toHaveBeenCalled();
	});
});
