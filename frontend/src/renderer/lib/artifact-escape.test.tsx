import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useArtifactFrameEscape } from "./artifact-escape";
import { aoBridge } from "./bridge";

let escape: (() => void) | undefined;

function Surface({ active, container, onEscape }: { active: boolean; container: () => Element | null; onEscape: () => void }) {
	useArtifactFrameEscape(active, container, onEscape);
	return null;
}

/**
 * Focus in a cross-origin framed page: Chromium leaves the frame as this
 * document's activeElement and sends it no focusin.
 */
function focusFrameIn(parent: Element, tag: "iframe" | "webview" = "iframe") {
	const frame = document.createElement(tag);
	parent.append(frame);
	Object.defineProperty(document, "activeElement", { configurable: true, get: () => frame });
	return frame;
}

describe("useArtifactFrameEscape", () => {
	beforeEach(() => {
		vi.spyOn(aoBridge.artifactEscape, "onEscape").mockImplementation((listener) => {
			escape = listener;
			return () => { if (escape === listener) escape = undefined; };
		});
	});

	afterEach(() => {
		delete (document as { activeElement?: Element | null }).activeElement;
		document.body.replaceChildren();
		escape = undefined;
		vi.restoreAllMocks();
	});

	it("calls back for Escape pressed in a framed page inside its container", () => {
		const container = document.createElement("div");
		document.body.append(container);
		const onEscape = vi.fn();
		render(<Surface active container={() => container} onEscape={onEscape} />);

		focusFrameIn(container);
		act(() => escape?.());
		expect(onEscape).toHaveBeenCalledOnce();

		focusFrameIn(container, "webview");
		act(() => escape?.());
		expect(onEscape).toHaveBeenCalledTimes(2);
	});

	it("leaves Escape with focus in this document, or in a frame elsewhere, to its own handlers", () => {
		const container = document.createElement("div");
		const button = document.createElement("button");
		container.append(button);
		document.body.append(container);
		const onEscape = vi.fn();
		render(<Surface active container={() => container} onEscape={onEscape} />);

		button.focus();
		act(() => escape?.());
		focusFrameIn(document.body);
		act(() => escape?.());
		expect(onEscape).not.toHaveBeenCalled();
	});

	it("does not listen while inactive", () => {
		const container = document.createElement("div");
		document.body.append(container);
		const onEscape = vi.fn();
		render(<Surface active={false} container={() => container} onEscape={onEscape} />);
		expect(escape).toBeUndefined();
	});

	it("keeps main reporting Escape until the last open surface closes", () => {
		const setArmed = vi.spyOn(aoBridge.artifactEscape, "setArmed");
		const container = () => null;
		const first = render(<Surface active container={container} onEscape={() => {}} />);
		const second = render(<Surface active container={container} onEscape={() => {}} />);
		expect(setArmed.mock.calls).toEqual([[true]]);

		first.unmount();
		expect(setArmed.mock.calls).toEqual([[true]]);
		second.unmount();
		expect(setArmed.mock.calls).toEqual([[true], [false]]);
	});
});
