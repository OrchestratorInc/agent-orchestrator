import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useWorkspaceTopbarBottom } from "./useWorkspaceTopbarBottom";

function mountBar(top: number, height: number): HTMLElement {
	const bar = document.createElement("header");
	bar.className = "workspace-topbar-container";
	bar.getBoundingClientRect = () => ({ top, bottom: top + height, height, left: 0, right: 0, width: 0, x: 0, y: top, toJSON: () => ({}) });
	document.body.append(bar);
	return bar;
}

describe("useWorkspaceTopbarBottom", () => {
	afterEach(() => {
		document.body.querySelectorAll(".workspace-topbar-container").forEach((bar) => bar.remove());
	});

	it("measures where the framed bar ends, inset included", () => {
		// Linux session: 14px inset + 8px drag strip + 1px border, then the 36px bar.
		mountBar(23, 36);
		const { result } = renderHook(() => useWorkspaceTopbarBottom());
		expect(result.current).toBe(59);
	});

	it("follows bars as routes mount and unmount them", async () => {
		const { result } = renderHook(() => useWorkspaceTopbarBottom());
		expect(result.current).toBeNull();
		let bar!: HTMLElement;
		act(() => { bar = mountBar(15, 36); });
		await waitFor(() => expect(result.current).toBe(51));
		act(() => bar.remove());
		await waitFor(() => expect(result.current).toBeNull());
	});

	it("ignores hidden bars and bars away from the top edge", () => {
		mountBar(0, 0);
		mountBar(window.innerHeight - 40, 36);
		mountBar(6, 36);
		const { result } = renderHook(() => useWorkspaceTopbarBottom());
		expect(result.current).toBe(42);
	});
});
