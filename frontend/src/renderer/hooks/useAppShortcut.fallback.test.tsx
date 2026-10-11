import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appShortcutBus } from "../test/app-shortcut-bus";
import { useAppShortcut } from "./useAppShortcut";

function App({ handler, enabled = true }: { handler: () => void; enabled?: boolean }) {
	useAppShortcut("command-palette", handler, enabled);
	return null;
}

// The window listener backs up the main process where none is attached (web
// preview, e2e). jsdom reports a non-mac platform, so the palette is Ctrl+K.
describe("useAppShortcut window fallback", () => {
	beforeEach(() => appShortcutBus.reset());

	const press = (init: KeyboardEventInit, target: Element | Window = window) => {
		const event = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
		act(() => {
			target.dispatchEvent(event);
		});
		return event;
	};

	it("runs the handler for the chord and consumes the event", () => {
		const handler = vi.fn();
		render(<App handler={handler} />);
		const event = press({ key: "k", ctrlKey: true });
		expect(handler).toHaveBeenCalledTimes(1);
		expect(event.defaultPrevented).toBe(true);
	});

	it("ignores other chords and events something else already handled", () => {
		const handler = vi.fn();
		render(<App handler={handler} />);
		press({ key: "j", ctrlKey: true });
		const prevented = new KeyboardEvent("keydown", { key: "k", ctrlKey: true, cancelable: true });
		prevented.preventDefault();
		act(() => {
			window.dispatchEvent(prevented);
		});
		expect(handler).not.toHaveBeenCalled();
	});

	it("lets every subscriber of the same shortcut run", () => {
		const first = vi.fn();
		const second = vi.fn();
		render(
			<>
				<App handler={first} />
				<App handler={second} />
			</>,
		);
		press({ key: "k", ctrlKey: true });
		expect(first).toHaveBeenCalledTimes(1);
		expect(second).toHaveBeenCalledTimes(1);
	});

	it("leaves plain Ctrl+K to a focused terminal but keeps the Shift alternative live", () => {
		const handler = vi.fn();
		render(<App handler={handler} />);
		const terminal = document.createElement("div");
		terminal.className = "xterm";
		document.body.append(terminal);
		press({ key: "k", ctrlKey: true }, terminal);
		expect(handler).not.toHaveBeenCalled();
		press({ key: "P", ctrlKey: true, shiftKey: true }, terminal);
		expect(handler).toHaveBeenCalledTimes(1);
		terminal.remove();
	});

	it("stops listening when disabled or unmounted", () => {
		const handler = vi.fn();
		const { rerender, unmount } = render(<App handler={handler} enabled={false} />);
		press({ key: "k", ctrlKey: true });
		expect(handler).not.toHaveBeenCalled();
		rerender(<App handler={handler} enabled />);
		unmount();
		press({ key: "k", ctrlKey: true });
		expect(handler).not.toHaveBeenCalled();
	});
});
