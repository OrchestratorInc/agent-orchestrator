import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { createPortal } from "react-dom";
import { expect, it, vi } from "vitest";
import { SettingsBrowserOverlay } from "./SettingsBrowserOverlay";

const openLink = vi.hoisted(() => vi.fn(async (_url: string) => undefined));
const useBrowserView = vi.hoisted(() => vi.fn(() => ({ openLink })));

vi.mock("../../hooks/useBrowserView", () => ({
	useBrowserView,
}));
vi.mock("../BrowserPanel", () => ({
	BrowserPanelView: () => <button>Browser action</button>,
	useBrowserAnnotationQueue: () => ({}),
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
	screen.getByRole("button", { name: "Browser action" }).focus();
	await user.tab();
	expect(screen.getByRole("button", { name: "Browser action" })).toHaveFocus();
	view.unmount();
	expect(shell).not.toHaveAttribute("inert");
});

function SettingsWithLogin({ context }: { context: "Home" | "active session" }) {
	const [browserOpen, setBrowserOpen] = useState(false);
	return <>
		<div data-testid="settings-shell">
			<button>{context}</button>
			<button onClick={() => setBrowserOpen(true)}>Login terminal</button>
		</div>
		{browserOpen ? createPortal(
			<SettingsBrowserOverlay request={{ id: 1, url: "https://app.cline.bot/auth/device" }} onClose={() => setBrowserOpen(false)} />,
			document.body,
		) : null}
	</>;
}

it.each(["Home", "active session"] as const)("restores focus to the login terminal after closing from %s", async (context) => {
	const user = userEvent.setup();
	render(<SettingsWithLogin context={context} />);
	const opener = screen.getByRole("button", { name: "Login terminal" });
	await user.click(opener);
	const shell = screen.getByTestId("settings-shell");
	expect(shell.parentElement).toHaveAttribute("inert");
	expect(screen.getByRole("dialog")).toHaveFocus();
	const focus = opener.focus.bind(opener);
	const inertDuringFocus: boolean[] = [];
	vi.spyOn(opener, "focus").mockImplementation((options) => {
		inertDuringFocus.push(shell.parentElement?.hasAttribute("inert") ?? false);
		focus(options);
	});
	await user.keyboard("{Escape}");
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(shell.parentElement).not.toHaveAttribute("inert");
	expect(opener).toHaveFocus();
	expect(inertDuringFocus).toEqual([false]);
});
