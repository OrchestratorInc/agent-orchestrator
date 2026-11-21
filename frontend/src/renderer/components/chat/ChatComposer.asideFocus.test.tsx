import { act, render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { ChatComposer } from "./ChatComposer";

function render(ui: ReactElement) {
	return rtlRender(ui, { wrapper: TooltipProvider });
}

// The aside pane's reply box (AsidePane) beside the conversation's composer.
// Opening a mention or an aside reply focuses it; the composer must leave the
// caret there. Live on staging the composer pulled it back about a second
// after an instant open, once its own "Checking what this session lets you
// do" state ended (disabled -> enabled re-ran its autofocus).
function Screen({ disabled }: { disabled: boolean }) {
	return (
		<>
			<div data-aside-reply-box="">
				<div aria-label="Reply in the aside" contentEditable role="textbox" suppressContentEditableWarning tabIndex={0} />
			</div>
			<ChatComposer autoFocusKey="session-1" disabled={disabled} onSend={vi.fn()} />
		</>
	);
}

beforeEach(() => {
	localStorage.clear();
	useUiStore.setState({ chatSendKeyMode: "enter" });
});

describe("the conversation's composer and the aside's reply box", () => {
	it("keeps the caret in the aside's reply box when the composer turns enabled", () => {
		const view = render(<Screen disabled />);
		const reply = screen.getByLabelText("Reply in the aside");
		act(() => reply.focus());
		expect(document.activeElement).toBe(reply);

		view.rerender(<Screen disabled={false} />);

		expect(document.activeElement).toBe(reply);
	});

	it("keeps the caret in the aside's reply box when the window regains focus", () => {
		render(<Screen disabled={false} />);
		const reply = screen.getByLabelText("Reply in the aside");
		act(() => reply.focus());

		act(() => {
			window.dispatchEvent(new Event("focus"));
		});

		expect(document.activeElement).toBe(reply);
	});

	it("still takes the caret itself when nothing else holds it", () => {
		const view = render(<Screen disabled />);
		expect(document.activeElement).not.toBe(screen.getByLabelText("Message the agent"));

		view.rerender(<Screen disabled={false} />);

		expect(document.activeElement).toBe(screen.getByLabelText("Message the agent"));
	});
});
