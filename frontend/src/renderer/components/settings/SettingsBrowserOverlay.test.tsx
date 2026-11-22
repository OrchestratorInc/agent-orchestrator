import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { expect, it, vi } from "vitest";
import { SettingsBrowserOverlay } from "./SettingsBrowserOverlay";

const openLink = vi.hoisted(() => vi.fn(async (_url: string) => undefined));
const useBrowserView = vi.hoisted(() => vi.fn(() => ({ openLink })));

vi.mock("../../hooks/useBrowserView", () => ({
	useBrowserView,
}));
vi.mock("../BrowserPanel", () => ({
	BrowserPanelView: () => null,
	useBrowserAnnotationQueue: () => ({}),
}));
vi.mock("../SessionBrowserPopOut", () => ({
	SessionBrowserPopOut: ({ children }: { children: ReactNode }) => <div role="dialog"><button>Browser first</button>{children}<button>Browser last</button></div>,
}));

it("opens device links in a Settings-owned AO Browser without a session", () => {
	const renderOverlay = (id: number, url: string) => <>
		<button>Login terminal</button>
		{createPortal(<SettingsBrowserOverlay request={{ id, url }} onClose={vi.fn()} />, document.body)}
	</>;
	const view = render(renderOverlay(1, "https://app.cline.bot/auth/device"));
	expect(useBrowserView).toHaveBeenCalledWith({ sessionId: "settings-login-browser", active: true, poppedOut: true, terminated: false });
	expect(openLink).toHaveBeenCalledExactlyOnceWith("https://app.cline.bot/auth/device");
	view.rerender(renderOverlay(2, "https://example.com/next"));
	expect(openLink).toHaveBeenLastCalledWith("https://example.com/next");
});

it("keeps the login terminal mounted and inert while focus stays in the Browser", async () => {
	const user = userEvent.setup();
	const view = render(<>
		<button>Login terminal</button>
		{createPortal(<SettingsBrowserOverlay request={{ id: 3, url: "https://app.cline.bot/auth/device" }} onClose={vi.fn()} />, document.body)}
	</>);
	const terminal = screen.getByRole("button", { name: "Login terminal", hidden: true });
	const shell = terminal.parentElement;
	expect(terminal).toBeInTheDocument();
	expect(shell).toHaveAttribute("inert");
	screen.getByRole("button", { name: "Browser last" }).focus();
	await user.tab();
	expect(screen.getByRole("button", { name: "Browser first" })).toHaveFocus();
	view.unmount();
	expect(shell).not.toHaveAttribute("inert");
});
