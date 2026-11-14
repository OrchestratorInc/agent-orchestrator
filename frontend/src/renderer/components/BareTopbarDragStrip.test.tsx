import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mockIsMac = vi.hoisted(() => vi.fn(() => true));

describe("BareTopbarDragStrip", () => {
	beforeEach(() => {
		vi.resetModules();
		vi.doMock("../lib/platform", async () => ({
			...(await vi.importActual<typeof import("../lib/platform")>("../lib/platform")),
			isMacPlatform: mockIsMac,
		}));
	});

	it("keeps the top edge draggable on macOS", async () => {
		mockIsMac.mockReturnValue(true);
		const { BareTopbarDragStrip } = await import("./BareTopbarDragStrip");
		render(<BareTopbarDragStrip />);
		const strip = screen.getByTestId("bare-topbar-drag-strip");
		expect(strip).toHaveAttribute("aria-hidden", "true");
		// React sets the vendor property directly, which jsdom does not reflect as CSS text.
		expect((strip.style as CSSStyleDeclaration & { WebkitAppRegion?: string }).WebkitAppRegion).toBe("drag");
	});

	it("renders nothing where the OS draws the title bar", async () => {
		mockIsMac.mockReturnValue(false);
		const { BareTopbarDragStrip } = await import("./BareTopbarDragStrip");
		render(<BareTopbarDragStrip />);
		expect(screen.queryByTestId("bare-topbar-drag-strip")).not.toBeInTheDocument();
	});
});
