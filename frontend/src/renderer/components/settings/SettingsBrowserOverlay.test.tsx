import { render } from "@testing-library/react";
import type { ReactNode } from "react";
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
	SessionBrowserPopOut: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

it("opens device links in a Settings-owned AO Browser without a session", () => {
	const view = render(<SettingsBrowserOverlay request={{ id: 1, url: "https://app.cline.bot/auth/device" }} onClose={vi.fn()} />);
	expect(useBrowserView).toHaveBeenCalledWith({ sessionId: "settings-login-browser", active: true, poppedOut: true, terminated: false });
	expect(openLink).toHaveBeenCalledExactlyOnceWith("https://app.cline.bot/auth/device");
	view.rerender(<SettingsBrowserOverlay request={{ id: 2, url: "https://example.com/next" }} onClose={vi.fn()} />);
	expect(openLink).toHaveBeenLastCalledWith("https://example.com/next");
});
