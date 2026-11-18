import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { beginPaneTabDrag, isPaneTabDragging, registerPaneDropTarget } from "./pane-drag";

describe("pane tab drag completion", () => {
	let source: HTMLButtonElement;
	let unregister: () => void;
	const select = vi.fn();
	const commit = vi.fn();
	const resolve = vi.fn((_key: string, _x: number, _y: number) => ({
		rect: { left: 0, top: 40, width: 400, height: 300 },
		commit,
	}));

	beforeEach(() => {
		vi.useFakeTimers();
		select.mockClear();
		commit.mockClear();
		resolve.mockClear();
		source = document.createElement("button");
		source.textContent = "Terminal";
		source.addEventListener("click", select);
		document.body.appendChild(source);
		vi.spyOn(source, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 120, 40));
		unregister = registerPaneDropTarget({ canDrag: () => true, resolve });
	});

	afterEach(() => {
		window.dispatchEvent(new Event("blur"));
		vi.runAllTimers();
		unregister();
		source.remove();
		vi.restoreAllMocks();
		vi.useRealTimers();
	});

	function holdTab() {
		beginPaneTabDrag({ pointerId: 1, clientX: 20, clientY: 20, button: 0, target: source }, "terminal", source);
		vi.advanceTimersByTime(320);
		expect(isPaneTabDragging()).toBe(true);
	}

	it.each(["Escape", "blur", "pointercancel"])("does not select a held tab when canceled with %s", (reason) => {
		holdTab();
		if (reason === "Escape") window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
		else if (reason === "blur") window.dispatchEvent(new Event("blur"));
		else window.dispatchEvent(new PointerEvent("pointercancel", { pointerId: 1 }));
		vi.runAllTimers();

		expect(isPaneTabDragging()).toBe(false);
		expect(select).not.toHaveBeenCalled();
		expect(commit).not.toHaveBeenCalled();
		expect(document.body.querySelector(".pane-drag-ghost")).toBeNull();
		expect(document.body.querySelector(".pane-drop-preview")).toBeNull();
	});

	it("selects a held tab once when released in place", () => {
		holdTab();
		window.dispatchEvent(new PointerEvent("pointerup", { pointerId: 1, clientX: 20, clientY: 20 }));
		// The browser's captured click is swallowed before the intended one is replayed.
		source.click();
		vi.runAllTimers();

		expect(select).toHaveBeenCalledTimes(1);
		expect(commit).not.toHaveBeenCalled();
	});

	it("sizes the ghost to the tab frame, not the wrapper that was pressed", () => {
		const frame = document.createElement("span");
		frame.setAttribute("data-terminal-tab-frame", "");
		source.appendChild(frame);
		vi.spyOn(frame, "getBoundingClientRect").mockReturnValue(new DOMRect(4, 2, 96, 34));
		holdTab();

		const ghost = document.body.querySelector<HTMLElement>(".pane-drag-ghost");
		expect(ghost?.style.width).toBe("96px");
		expect(ghost?.style.height).toBe("34px");
		expect(ghost?.firstElementChild?.hasAttribute("data-terminal-tab-frame")).toBe(true);
	});

	it("drops at the release coordinates even without another pointer move", () => {
		holdTab();
		window.dispatchEvent(new PointerEvent("pointerup", { pointerId: 1, clientX: 300, clientY: 200 }));
		vi.runAllTimers();

		expect(resolve).toHaveBeenLastCalledWith("terminal", 300, 200);
		expect(commit).toHaveBeenCalledTimes(1);
		expect(select).not.toHaveBeenCalled();
	});
});
