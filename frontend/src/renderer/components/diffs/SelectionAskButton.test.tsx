import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SelectionAskButton } from "./SelectionAskButton";
import type { CodeSelection, CodeSelectionSource } from "./useCodeSelection";
import { isMacPlatform } from "../../lib/platform";

vi.mock("../../lib/platform", () => ({ isMacPlatform: vi.fn(() => false) }));
beforeEach(() => vi.mocked(isMacPlatform).mockReturnValue(false));
afterEach(() => {
	document.querySelectorAll("[data-test-selection-scope]").forEach(element => element.remove());
	vi.restoreAllMocks();
});

function source(selection: Partial<CodeSelection> | null) {
	return { get: () => selection as CodeSelection | null, subscribe: () => () => {} };
}

function keyboardSelection() {
	const scope = document.createElement("div");
	scope.className = "ao-pierre-surface"; scope.setAttribute("data-test-selection-scope", "");
	const host = document.createElement("diffs-container"); scope.append(host); document.body.append(scope);
	const root = host.attachShadow({ mode: "open" }); root.textContent = "selected code";
	const text = root.firstChild!;
	let live = true, endOffset = 8, top = 80;
	Object.assign(root, { getSelection: () => live ? {
		anchorNode: text, anchorOffset: 0, focusNode: text, focusOffset: endOffset,
		removeAllRanges: () => { live = false; },
	} : null });
	const range = document.createRange(); range.setStart(text, 0); range.setEnd(text, 8);
	vi.spyOn(range, "getBoundingClientRect").mockImplementation(() => new DOMRect(80, top, 120, 20));
	vi.spyOn(scope, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 600, 400));
	let selection: CodeSelection | null = { host, textRange: range, viewport: scope,
		rows: [], range: { start: 1, end: 1 }, point: { x: 200, y: 90 }, dragging: false };
	const listeners = new Set<() => void>();
	const store: CodeSelectionSource = { get: () => selection, subscribe: listener => { listeners.add(listener); return () => listeners.delete(listener); } };
	const dismiss = vi.fn(() => { selection = null; for (const listener of listeners) listener(); });
	return { scope, host, store, dismiss, offscreen() { top = -100; }, changed() { endOffset = 4; }, clearNative() { live = false; } };
}

describe("SelectionAskButton", () => {
	it("reports a rejected click and keyboard request without discarding the selection", () => {
		const onAsk = vi.fn(() => false);
		const selected = keyboardSelection();
		render(<SelectionAskButton onAsk={onAsk} source={selected.store} />);
		fireEvent.click(screen.getByRole("button", { name: "Add to chat" }));
		expect(screen.getByRole("alert")).toHaveTextContent("Selection not added");
		expect(selected.store.get()).not.toBeNull();
		expect(fireEvent.keyDown(document, { key: "l", ctrlKey: true })).toBe(false);
		expect(onAsk).toHaveBeenCalledTimes(2);
		expect(screen.getByRole("button", { name: "Add to chat" })).toBeVisible();
	});

	it.each(["Win32", "Linux x86_64", "MacIntel"])("uses only the owning surface's primary shortcut on %s", (platform) => {
		vi.mocked(isMacPlatform).mockReturnValue(platform === "MacIntel");
		const selected = keyboardSelection(), onAsk = vi.fn();
		render(<SelectionAskButton onAsk={onAsk} onDismiss={selected.dismiss} source={selected.store} />);
		const chord = { key: "l", ctrlKey: platform !== "MacIntel", metaKey: platform === "MacIntel" };
		expect(fireEvent.keyDown(selected.host, { ...chord, ctrlKey: !chord.ctrlKey, metaKey: !chord.metaKey })).toBe(true);
		expect(fireEvent.keyDown(selected.host, { ...chord, repeat: true })).toBe(false);
		expect(onAsk).not.toHaveBeenCalled();
		expect(fireEvent.keyDown(selected.host, chord)).toBe(false);
		expect(onAsk).toHaveBeenCalledOnce();
	});

	it("leaves Escape and Ctrl+L to other inputs, terminals and panes", () => {
		const selected = keyboardSelection(), onAsk = vi.fn();
		render(<SelectionAskButton onAsk={onAsk} onDismiss={selected.dismiss} source={selected.store} />);
		const targets = [document.createElement("input"), document.createElement("textarea"), document.createElement("div"), document.createElement("div"), document.createElement("button")];
		targets[2].setAttribute("contenteditable", "true"); targets[3].className = "xterm";
		for (const target of targets) {
			document.body.append(target);
			expect(fireEvent.keyDown(target, { key: "Escape" })).toBe(true);
			expect(fireEvent.keyDown(target, { key: "l", ctrlKey: true })).toBe(true);
			target.remove();
		}
		expect(onAsk).not.toHaveBeenCalled(); expect(selected.dismiss).not.toHaveBeenCalled();
	});

	it.each(["Escape", "l"])("handles %s when Pierre focuses its enclosing code surface", key => {
		const selected = keyboardSelection(), onAsk = vi.fn(() => false);
		render(<SelectionAskButton onAsk={onAsk} onDismiss={selected.dismiss} source={selected.store} />);
		selected.scope.tabIndex = -1; act(() => selected.scope.focus());
		expect(fireEvent.keyDown(selected.scope, { key, ctrlKey: key === "l" })).toBe(false);
		if (key === "Escape") expect(selected.dismiss).toHaveBeenCalledOnce();
		else expect(onAsk).toHaveBeenCalledOnce();
	});

	it("does not use a retained selection while its toolbar is offscreen or its live range changed", () => {
		const selected = keyboardSelection(), onAsk = vi.fn();
		render(<SelectionAskButton onAsk={onAsk} onDismiss={selected.dismiss} source={selected.store} />);
		selected.offscreen(); // Before the queued geometry frame updates CSS.
		expect(fireEvent.keyDown(selected.host, { key: "l", ctrlKey: true })).toBe(true);
		expect(fireEvent.keyDown(selected.host, { key: "Escape" })).toBe(true);
		expect(onAsk).not.toHaveBeenCalled(); expect(selected.dismiss).not.toHaveBeenCalled();
	});

	it("ignores changed or detached selections before their observer runs", () => {
		const selected = keyboardSelection(), onAsk = vi.fn();
		render(<SelectionAskButton onAsk={onAsk} onDismiss={selected.dismiss} source={selected.store} />);
		selected.changed();
		expect(fireEvent.keyDown(selected.host, { key: "l", ctrlKey: true })).toBe(true);
		expect(fireEvent.keyDown(selected.host, { key: "Escape" })).toBe(true);
		selected.host.remove();
		expect(fireEvent.keyDown(document, { key: "l", ctrlKey: true })).toBe(true);
		expect(onAsk).not.toHaveBeenCalled(); expect(selected.dismiss).not.toHaveBeenCalled();
	});

	it.each(["dialog", "menu"])("preserves an open %s and already-handled keys", role => {
		const selected = keyboardSelection(), onAsk = vi.fn();
		render(<SelectionAskButton onAsk={onAsk} onDismiss={selected.dismiss} source={selected.store} />);
		const overlay = document.createElement("div"); overlay.setAttribute("role", role); overlay.dataset.state = "open";
		document.body.append(overlay);
		expect(fireEvent.keyDown(selected.host, { key: "Escape" })).toBe(true);
		expect(fireEvent.keyDown(selected.host, { key: "l", ctrlKey: true })).toBe(true);
		overlay.remove();
		const handled = new KeyboardEvent("keydown", { key: "l", ctrlKey: true, bubbles: true, cancelable: true });
		handled.preventDefault(); selected.host.dispatchEvent(handled);
		expect(onAsk).not.toHaveBeenCalled(); expect(selected.dismiss).not.toHaveBeenCalled();
	});

	it("consumes dismissal and held Escape, then releases the separate next press", () => {
		const selected = keyboardSelection();
		render(<SelectionAskButton onAsk={vi.fn()} onDismiss={selected.dismiss} source={selected.store} />);
		expect(fireEvent.keyDown(selected.host, { key: "Escape" })).toBe(false);
		expect(selected.dismiss).toHaveBeenCalledOnce();
		expect(fireEvent.keyDown(document, { key: "Escape", repeat: true })).toBe(false);
		fireEvent.keyUp(document, { key: "Escape" });
		expect(fireEvent.keyDown(document, { key: "Escape" })).toBe(true);
	});

	it("allows a focused toolbar to dismiss after native selection collapses", () => {
		const selected = keyboardSelection();
		render(<SelectionAskButton onAsk={vi.fn()} onDismiss={selected.dismiss} source={selected.store} />);
		const button = screen.getByRole("button", { name: "Add to chat" }); act(() => button.focus());
		selected.clearNative();
		expect(fireEvent.keyDown(button, { key: "Escape" })).toBe(false);
		expect(selected.dismiss).toHaveBeenCalledOnce();
	});
	it("shows nothing without a selection", () => {
		render(<SelectionAskButton onAsk={vi.fn()} source={source(null)} />);
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("asks in chat without taking the selection away", () => {
		const onAsk = vi.fn();
		render(<SelectionAskButton onAsk={onAsk} source={source({ range: { start: 2, end: 4 }, point: { x: 120, y: 80 }, dragging: false })} />);
		const button = screen.getByRole("button", { name: "Add to chat" });
		expect(fireEvent.mouseDown(button)).toBe(false);
		fireEvent.click(button);
		fireEvent.click(button);
		expect(onAsk).toHaveBeenCalledTimes(1);
		expect(button.querySelector("svg")).toBeNull();
	});

	it("allows retry when the selection handler refuses the action", () => {
		const onAsk = vi.fn(() => false);
		render(<SelectionAskButton onAsk={onAsk} source={source({ range: { start: 2, end: 4 }, point: { x: 120, y: 80 }, dragging: false })} />);
		const button = screen.getByRole("button", { name: "Add to chat" });
		fireEvent.click(button); fireEvent.click(button);
		expect(button).toBeEnabled(); expect(onAsk).toHaveBeenCalledTimes(2);
	});

	it("sticks below the filename header instead of covering its controls", () => {
		const host = document.createElement("div"); host.textContent = "selected code";
		const header = document.createElement("div"); document.body.append(header, host);
		const range = document.createRange(); range.selectNodeContents(host);
		vi.spyOn(range, "getBoundingClientRect").mockReturnValue(new DOMRect(20, -100, 400, 2000));
		vi.spyOn(header, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 600, 48));
		const view = render(<SelectionAskButton onAsk={vi.fn()} source={source({ host, textRange: range, toolbarHeader: header, range: { start: 1, end: 80 }, point: { x: 20, y: 1900 }, dragging: false })} />);
		expect(screen.getByRole("button", { name: "Add to chat" }).parentElement).toHaveStyle({ top: "56px" });
		view.unmount(); host.remove(); header.remove();
	});

	it("lets a selection still being dragged pass through it", () => {
		render(<SelectionAskButton onAsk={vi.fn()} source={source({ range: { start: 2, end: 4 }, point: { x: 0, y: 0 }, dragging: true })} />);
		expect(screen.getByRole("button", { name: "Add to chat" }).parentElement).toHaveStyle({ pointerEvents: "none" });
	});
});
