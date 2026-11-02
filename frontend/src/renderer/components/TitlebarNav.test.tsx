import { render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { TitlebarNav } from "./TitlebarNav";
import { TooltipProvider } from "./ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

const { history } = vi.hoisted(() => ({
	history: {
		back: vi.fn(),
		forward: vi.fn(),
		location: { state: { __TSR_index: 0 } },
		subscribe: vi.fn(() => () => undefined),
	},
}));

vi.mock("@tanstack/react-router", () => ({
	useCanGoBack: () => false,
	useNavigate: () => vi.fn(),
	useRouter: () => ({ history }),
}));

vi.mock("../lib/platform", () => ({
	isLinuxPlatform: () => false,
	isMacPlatform: () => true,
}));

describe("TitlebarNav", () => {
	beforeEach(() => {
		useUiStore.setState({ isSidebarOpen: true });
	});

	it.each([false, true])("keeps the same header row with sidebar open=%s", (open) => {
		useUiStore.setState({ isSidebarOpen: open });
		const { container, rerender } = render(<TitlebarNav />);
		const nav = container.querySelector('[data-slot="titlebar-nav"]');
		expect(nav).toHaveClass("top-0", "h-traffic-light-clearance", "left-titlebar-cluster-left");
		rerender(<TooltipProvider><TitlebarNav isFullScreen /></TooltipProvider>);
		expect(nav).toHaveClass("top-0", "h-toolbar", "left-titlebar-cluster-left-fullscreen");
		// The brand and the history arrows trade places; the hidden one is inert and
		// aria-hidden (it collapses to width 0 rather than unmounting).
		const brand = container.querySelector('[data-slot="titlebar-brand"]');
		const history = container.querySelector('[data-slot="titlebar-history"]');
		expect(brand?.hasAttribute("aria-hidden")).toBe(!open);
		expect(history?.hasAttribute("aria-hidden")).toBe(open);
		if (!open) expect(screen.getByRole("button", { name: "Go back" })).toBeDisabled();
	});

	it("nudges the sidebar re-clamp when fullscreen or zoom moves the brand", () => {
		const onResize = vi.fn();
		window.addEventListener("resize", onResize);
		const { rerender } = render(<TitlebarNav />);
		expect(onResize).toHaveBeenCalledTimes(1);
		rerender(<TooltipProvider><TitlebarNav isFullScreen /></TooltipProvider>);
		expect(onResize).toHaveBeenCalledTimes(2);
		rerender(<TooltipProvider><TitlebarNav zoomFactor={1.25} /></TooltipProvider>);
		expect(onResize).toHaveBeenCalledTimes(3);
		// No brand to fit while the sidebar is closed.
		useUiStore.setState({ isSidebarOpen: false });
		rerender(<TooltipProvider><TitlebarNav isFullScreen zoomFactor={1.25} /></TooltipProvider>);
		expect(onResize).toHaveBeenCalledTimes(3);
		window.removeEventListener("resize", onResize);
	});
});
