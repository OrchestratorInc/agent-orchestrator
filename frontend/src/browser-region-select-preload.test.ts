import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";

const electronMocks = vi.hoisted(() => {
	const listeners = new Map<string, (...args: unknown[]) => void>();
	return {
		listeners,
		on: vi.fn((channel: string, listener: (...args: unknown[]) => void) => listeners.set(channel, listener)),
		send: vi.fn(),
	};
});

vi.mock("electron", () => ({
	ipcRenderer: { on: electronMocks.on, send: electronMocks.send },
}));

const { installRegionSelect } = await import("./browser-region-select-preload");
installRegionSelect();

function setMode(enabled: boolean): void {
	const listener = electronMocks.listeners.get("browser:regionSelect:setMode");
	if (!listener) throw new Error("region select listener was not registered");
	listener({}, { enabled });
}

function overlay(): HTMLElement | null {
	return document.querySelector("[data-ao-region-select]");
}

function pointer(type: "pointerdown" | "pointermove" | "pointerup", x: number, y: number): void {
	overlay()?.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientX: x, clientY: y }));
}

describe("region select overlay", () => {
	beforeEach(() => {
		Object.defineProperty(window, "innerWidth", { configurable: true, value: 800 });
		Object.defineProperty(window, "innerHeight", { configurable: true, value: 600 });
		electronMocks.send.mockClear();
	});

	afterEach(() => {
		setMode(false);
	});

	it("covers the page while on and leaves without a trace when turned off", () => {
		setMode(true);
		expect(overlay()).not.toBeNull();
		expect(overlay()?.shadowRoot?.querySelector(".hint")?.textContent).toContain("Esc");
		setMode(false);
		expect(overlay()).toBeNull();
		expect(electronMocks.send).not.toHaveBeenCalled();
	});

	it("reports the dragged rectangle after removing itself from the page", async () => {
		setMode(true);
		pointer("pointerdown", 300, 220);
		pointer("pointermove", 150, 100);
		const size = overlay()?.shadowRoot?.querySelector<HTMLElement>(".size");
		expect(size?.textContent).toBe("150 × 120");
		pointer("pointerup", 120, 60);
		expect(overlay()).toBeNull();
		await waitFor(() =>
			expect(electronMocks.send).toHaveBeenCalledWith("browser:regionSelect:select", {
				rect: { x: 120, y: 60, width: 180, height: 160 },
			}),
		);
	});

	it("cancels on a click without a drag", () => {
		setMode(true);
		pointer("pointerdown", 40, 40);
		pointer("pointerup", 42, 41);
		expect(overlay()).toBeNull();
		expect(electronMocks.send).toHaveBeenCalledWith("browser:regionSelect:cancel");
	});

	it("cancels on Escape without letting the page see the key", () => {
		const pageKey = vi.fn();
		document.addEventListener("keydown", pageKey);
		setMode(true);
		document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
		expect(overlay()).toBeNull();
		expect(electronMocks.send).toHaveBeenCalledWith("browser:regionSelect:cancel");
		expect(pageKey).not.toHaveBeenCalled();
		document.removeEventListener("keydown", pageKey);
	});
});
