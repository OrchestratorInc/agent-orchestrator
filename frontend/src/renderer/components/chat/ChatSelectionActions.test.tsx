import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { ChatSelectionActions } from "./ChatSelectionActions";

vi.mock("../../hooks/useChatSelectionPosition", () => ({ useChatSelectionPosition: () => ({ left: 8, top: 8, visible: true }) }));

function setup() {
	const range = document.createRange();
	const excerpt = { id: "selection", conversationId: "parent", messageId: "message", revision: 4, text: "exact selected text", role: "assistant" as const };
	return { selection: { range, excerpt }, viewportRef: { current: null }, onInvalid: vi.fn(), onAdd: vi.fn() };
}

describe("chat selection actions", () => {
	it.each(["{Enter}", " "])("supports native keyboard activation with %s", async (key) => {
		const props = setup(); render(<ChatSelectionActions {...props} />);
		const user = userEvent.setup();
		await user.tab(); await user.keyboard(key);
		expect(props.onAdd).toHaveBeenCalledTimes(1);
	});
	it("shows both text-only actions together while checking, then enables the side action", () => {
		const props = setup(); const onSide = vi.fn();
		const view = render(<ChatSelectionActions {...props} checkingSide />);
		expect(screen.getByRole("button", { name: "Add to side chat" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Add to chat" })).toBeEnabled();
		expect(view.container.querySelector("svg")).toBeNull();
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
