import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appShortcutBus } from "../test/app-shortcut-bus";
import { useUiStore } from "../stores/ui-store";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { useAppShortcut, useSessionShortcut } from "./useAppShortcut";
import type { AppShortcutId } from "../../shared/shortcuts";

vi.mock("../lib/platform", () => ({ isMacPlatform: () => false }));

const ID = "command-palette" as AppShortcutId;
const OTHER = "quick-open-file" as AppShortcutId;

function FindShortcut({ handler, shouldHandle }: { handler: () => void; shouldHandle?: () => boolean }) {
	useAppShortcut("find-in-chat", handler, true, shouldHandle);
	return null;
}

function App({ handler, enabled = true }: { handler: () => void; enabled?: boolean }) {
	useAppShortcut(ID, handler, enabled);
	return null;
}
function Session({ handler, enabled = true }: { handler: () => void; enabled?: boolean }) {
	useSessionShortcut(ID, handler, enabled);
	return null;
}

const emit = (id: AppShortcutId = ID) => act(() => appShortcutBus.emit({ id } as never));

describe("useAppShortcut", () => {
	beforeEach(() => appShortcutBus.reset());
	afterEach(() => useUiStore.setState({ settingsModal: null }));

	it("runs the handler only for its own id", () => {
		const handler = vi.fn();
		render(<App handler={handler} />);
		emit(OTHER);
		expect(handler).not.toHaveBeenCalled();
		emit();
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it("uses the latest handler without resubscribing", () => {
		const first = vi.fn();
		const second = vi.fn();
		const { rerender } = render(<App handler={first} />);
		rerender(<App handler={second} />);
		emit();
		expect(first).not.toHaveBeenCalled();
		expect(second).toHaveBeenCalledTimes(1);
	});

	it("unsubscribes on unmount", () => {
		const handler = vi.fn();
		const { unmount } = render(<App handler={handler} />);
		unmount();
		emit();
		expect(handler).not.toHaveBeenCalled();
	});

	it("unsubscribes while disabled and resubscribes when re-enabled", () => {
		const handler = vi.fn();
		const { rerender } = render(<App handler={handler} enabled={false} />);
		emit();
		expect(handler).not.toHaveBeenCalled();
		rerender(<App handler={handler} enabled />);
		emit();
		expect(handler).toHaveBeenCalledTimes(1);
		rerender(<App handler={handler} enabled={false} />);
		emit();
		expect(handler).toHaveBeenCalledTimes(1);
	});
});

describe("useAppShortcut terminal yield (Windows/Linux)", () => {
	beforeEach(() => {
		appShortcutBus.reset();
		useKeybindingsStore.setState({ overrides: {} });
	});
	afterEach(() => {
		useKeybindingsStore.setState({ overrides: {} });
		document.querySelector(".xterm")?.remove();
	});

	function terminal() {
		const el = document.createElement("div");
		el.className = "xterm";
		document.body.append(el);
		return el;
	}
	function press(target: EventTarget, init: KeyboardEventInit) {
		act(() => {
			target.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init }));
		});
	}

	it("leaves plain Ctrl+F to the terminal and runs Ctrl+Shift+F", () => {
		const handler = vi.fn();
		render(<FindShortcut handler={handler} />);
		const term = terminal();
		press(term, { key: "f", code: "KeyF", ctrlKey: true });
		expect(handler).not.toHaveBeenCalled();
		press(term, { key: "F", code: "KeyF", ctrlKey: true, shiftKey: true });
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it("runs a chord the user bound themselves even from the terminal", () => {
		useKeybindingsStore.setState({
			overrides: { "find-in-chat": [{ key: "j", code: undefined, ctrl: true, meta: false, shift: false, alt: false }] },
		});
		const handler = vi.fn();
		render(<FindShortcut handler={handler} />);
		const term = terminal();
		const xtermKeyDown = vi.fn((event: KeyboardEvent) => event.stopPropagation());
		term.addEventListener("keydown", xtermKeyDown);
		press(term, { key: "f", code: "KeyF", ctrlKey: true });
		expect(handler).not.toHaveBeenCalled();
		expect(xtermKeyDown).toHaveBeenCalledTimes(1);
		press(term, { key: "j", code: "KeyJ", ctrlKey: true });
		expect(handler).toHaveBeenCalledTimes(1);
		expect(xtermKeyDown).toHaveBeenCalledTimes(1);
	});

	it("leaves custom Find input to xterm when no transcript can handle it", () => {
		useKeybindingsStore.setState({
			overrides: { "find-in-chat": [{ key: "j", ctrl: true, meta: false, shift: false, alt: false }] },
		});
		const handler = vi.fn();
		render(<FindShortcut handler={handler} shouldHandle={() => false} />);
		const term = terminal();
		const xtermKeyDown = vi.fn();
		term.addEventListener("keydown", xtermKeyDown);
		press(term, { key: "j", code: "KeyJ", ctrlKey: true });
		expect(handler).not.toHaveBeenCalled();
		expect(xtermKeyDown).toHaveBeenCalledTimes(1);
	});
});

describe("useSessionShortcut", () => {
	beforeEach(() => appShortcutBus.reset());
	afterEach(() => useUiStore.setState({ settingsModal: null, archiveModal: null }));

	it("runs when no settings page is open", () => {
		const handler = vi.fn();
		render(<Session handler={handler} />);
		emit();
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it("stands down while the global settings page is open", () => {
		const handler = vi.fn();
		render(<Session handler={handler} />);
		useUiStore.setState({ settingsModal: { scope: "global" } as never });
		emit();
		expect(handler).not.toHaveBeenCalled();
		useUiStore.setState({ settingsModal: null });
		emit();
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it("stands down while the archive modal covers the page", () => {
		const handler = vi.fn();
		render(<Session handler={handler} />);
		act(() => useUiStore.getState().openArchive({ projectId: "p" }));
		emit();
		expect(handler).not.toHaveBeenCalled();
		act(() => useUiStore.getState().closeArchive());
		emit();
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it("still runs for project-scoped settings", () => {
		const handler = vi.fn();
		render(<Session handler={handler} />);
		useUiStore.setState({ settingsModal: { scope: "project", projectId: "p" } as never });
		emit();
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it("does not run when disabled", () => {
		const handler = vi.fn();
		render(<Session handler={handler} enabled={false} />);
		emit();
		expect(handler).not.toHaveBeenCalled();
	});
});
