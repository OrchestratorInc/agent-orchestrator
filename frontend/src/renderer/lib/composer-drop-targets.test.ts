import { afterEach, describe, expect, it } from "vitest";
import { composerDropTargetFor, noteComposerDropTargetFocus, registerComposerDropTarget, type ComposerDropTarget } from "./composer-drop-targets";

const cleanups: Array<() => void> = [];
afterEach(() => {
	for (const cleanup of cleanups.splice(0)) cleanup();
	document.body.innerHTML = "";
});

function element(parent: HTMLElement, visible = true): HTMLElement {
	const node = document.createElement("div");
	parent.appendChild(node);
	// jsdom lays nothing out; a rendered element has at least one client rect.
	if (visible) node.getClientRects = () => [{}] as unknown as DOMRectList;
	return node;
}

function composer(form: HTMLElement): ComposerDropTarget {
	const target: ComposerDropTarget = { element: () => form, accepts: () => true, attach: () => true, setDragging: () => undefined };
	cleanups.push(registerComposerDropTarget(target));
	return target;
}

describe("composerDropTargetFor", () => {
	it("picks the composer in the pane the drop landed in", () => {
		const left = element(document.body);
		const right = element(document.body);
		const leftComposer = composer(element(left));
		const rightComposer = composer(element(right));
		expect(composerDropTargetFor(element(left))).toBe(leftComposer);
		expect(composerDropTargetFor(element(right))).toBe(rightComposer);
	});

	it("falls back to the last focused composer when the drop is outside every pane", () => {
		const sidebar = element(document.body);
		const first = composer(element(element(document.body)));
		const second = composer(element(element(document.body)));
		noteComposerDropTargetFocus(second);
		expect(composerDropTargetFor(sidebar)).toBe(second);
		noteComposerDropTargetFocus(first);
		expect(composerDropTargetFor(sidebar)).toBe(first);
	});

	it("skips composers in inert or aria-hidden panes", () => {
		const shown = composer(element(document.body));
		const hiddenPane = element(document.body);
		hiddenPane.setAttribute("inert", "");
		composer(element(hiddenPane));
		const behindModal = element(document.body);
		behindModal.setAttribute("aria-hidden", "true");
		composer(element(behindModal));
		expect(composerDropTargetFor(element(hiddenPane))).toBe(shown);
		expect(composerDropTargetFor(element(behindModal))).toBe(shown);
	});

	it("ignores hidden and unregistered composers", () => {
		const pane = element(document.body);
		composer(element(pane, false));
		expect(composerDropTargetFor(element(pane))).toBeUndefined();
		const visible = composer(element(document.body));
		expect(composerDropTargetFor(element(pane))).toBe(visible);
		cleanups.splice(0).forEach((cleanup) => cleanup());
		expect(composerDropTargetFor(element(pane))).toBeUndefined();
	});
});
