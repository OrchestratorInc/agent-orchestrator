import { render, fireEvent } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SplitDivider } from "./SplitView";

vi.mock("./TerminalPane", () => ({ TerminalPane: () => null }));

afterEach(() => {
	vi.restoreAllMocks();
	vi.useRealTimers();
});

it("clears resize state and cancels the pending frame when a divider unmounts mid-drag", () => {
	vi.useFakeTimers();
	const screen = document.createElement("div");
	screen.style.setProperty("--split-r", "0.5");
	vi.spyOn(screen, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 800, 600));
	const onResize = vi.fn();
	const { getByRole, unmount } = render(
		<SplitDivider
			screenRef={{ current: screen }}
			divider={{
				path: "r", dir: "column", ratio: 0.5,
				box: { x: 0, y: 0, w: 1, h: 1 },
				style: { left: "0%", top: "50%", width: "100%", height: "" },
			}}
			onResize={onResize}
		/>,
	);
	const divider = getByRole("separator");
	fireEvent.pointerDown(divider, { button: 0, pointerId: 1, clientX: 400, clientY: 300 });
	fireEvent.pointerMove(divider, { pointerId: 1, clientX: 400, clientY: 400 });
	expect(document.body).toHaveClass("is-resizing-x");
	expect(document.body.style.cursor).toBe("row-resize");

	unmount();
	vi.runAllTimers();

	expect(document.body).not.toHaveClass("is-resizing-x");
	expect(document.body.style.cursor).toBe("");
	expect(screen.style.getPropertyValue("--split-r")).toBe("0.5");
	expect(onResize).not.toHaveBeenCalled();
});
