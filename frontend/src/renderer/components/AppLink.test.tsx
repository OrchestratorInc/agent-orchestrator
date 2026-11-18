import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AppBrowserLinkContext, AppLink } from "./AppLink";
import { ProductExternalLink } from "./ProductExternalLink";
import { aoBridge } from "../lib/bridge";

const url = "https://github.com/aoagents/agent-orchestrator/pull/42";
afterEach(() => vi.restoreAllMocks());

describe("AppLink", () => {
	it("opens PR links in ao even when the product link stops propagation", () => {
		const open = vi.fn();
		const external = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<AppBrowserLinkContext.Provider value={open}>
			<ProductExternalLink href={url} stopPropagation>PR #42</ProductExternalLink>
		</AppBrowserLinkContext.Provider>);
		fireEvent.click(screen.getByRole("link"));
		expect(open).toHaveBeenCalledExactlyOnceWith(url);
		expect(external).not.toHaveBeenCalled();
	});

	it("offers ao, external, and copy actions without opening on right-click", async () => {
		const user = userEvent.setup();
		const open = vi.fn();
		render(<AppBrowserLinkContext.Provider value={open}><AppLink href={url}>PR</AppLink></AppBrowserLinkContext.Provider>);
		fireEvent.contextMenu(screen.getByRole("link"));
		expect(open).not.toHaveBeenCalled();
		expect(screen.getAllByRole("menuitem").map(item => item.textContent)).toEqual([
			"Open in Orchestrator Browser", "Open in external browser", "Copy link",
		]);
		await user.click(screen.getByRole("menuitem", { name: "Open in Orchestrator Browser" }));
		expect(open).toHaveBeenCalledExactlyOnceWith(url);
	});

	it("falls back externally and disables ao when no active browser is available", () => {
		const external = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<AppLink href={url}>PR</AppLink>);
		fireEvent.click(screen.getByRole("link"));
		expect(external).toHaveBeenCalledExactlyOnceWith(url);
		fireEvent.contextMenu(screen.getByRole("link"));
		expect(screen.getByRole("menuitem", { name: "Open in Orchestrator Browser" })).toHaveAttribute("aria-disabled", "true");
	});

	it("sends system-browser links to the OS on every click and never to ao", async () => {
		const user = userEvent.setup();
		const open = vi.fn();
		const external = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<AppBrowserLinkContext.Provider value={open}>
			<AppLink href={url} systemBrowser target="_blank">PR</AppLink>
		</AppBrowserLinkContext.Provider>);
		const link = screen.getByRole("link", { name: "PR" });

		// Enter on a focused link dispatches this same click event.
		expect(fireEvent.click(link)).toBe(false);
		expect(fireEvent.click(link, { metaKey: true })).toBe(false);
		expect(fireEvent.click(link, { ctrlKey: true })).toBe(false);
		const middle = new MouseEvent("auxclick", { bubbles: true, cancelable: true, button: 1 });
		fireEvent(link, middle);
		expect(middle.defaultPrevented).toBe(true);
		expect(external).toHaveBeenCalledTimes(4);
		expect(external.mock.calls.every(([target]) => target === url)).toBe(true);
		expect(open).not.toHaveBeenCalled();

		fireEvent.contextMenu(link);
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Open in external browser", "Copy link",
		]);
		await user.click(screen.getByRole("menuitem", { name: "Open in external browser" }));
		expect(external).toHaveBeenCalledTimes(5);
		expect(open).not.toHaveBeenCalled();
	});

	it("opens nothing for system-browser links outside http(s) and mailto", () => {
		const open = vi.fn();
		const external = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<AppBrowserLinkContext.Provider value={open}>
			<AppLink href="file:///etc/hosts" systemBrowser>File</AppLink>
			<AppLink href="app://settings" systemBrowser>App</AppLink>
			<AppLink href="mailto:team@example.com" systemBrowser>Mail</AppLink>
		</AppBrowserLinkContext.Provider>);

		expect(fireEvent.click(screen.getByRole("link", { name: "File" }))).toBe(false);
		expect(fireEvent.click(screen.getByRole("link", { name: "App" }))).toBe(false);
		fireEvent.click(screen.getByRole("link", { name: "Mail" }));
		expect(external).toHaveBeenCalledExactlyOnceWith("mailto:team@example.com");
		expect(open).not.toHaveBeenCalled();
	});

	it("preserves handlers that cancel navigation and fragment links", () => {
		const open = vi.fn();
		const handled = vi.fn((event) => event.preventDefault());
		render(<AppBrowserLinkContext.Provider value={open}>
			<AppLink href={url} onClick={handled}>Custom</AppLink>
			<AppLink href="#section">Section</AppLink>
		</AppBrowserLinkContext.Provider>);
		fireEvent.click(screen.getByRole("link", { name: "Custom" }));
		fireEvent.click(screen.getByRole("link", { name: "Section" }));
		expect(handled).toHaveBeenCalledOnce();
		expect(open).not.toHaveBeenCalled();
	});

	it("offers Files for verified workspace paths without offering the system browser", async () => {
		const user = userEvent.setup();
		const open = vi.fn();
		const openFile = vi.fn();
		const path = "reports/final.html";
		render(
			<AppLink
				href="C:\\worktree\\reports\\final.html"
				onBrowserOpen={open}
				inAppLink={() => true}
				filePath={path}
				onFileOpen={openFile}
			>
				Report
			</AppLink>,
		);

		fireEvent.contextMenu(screen.getByRole("link", { name: "Report" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Open in Orchestrator Browser",
			"Open in Orchestrator",
			"Copy link",
		]);
		await user.click(screen.getByRole("menuitem", { name: "Open in Orchestrator" }));
		expect(openFile).toHaveBeenCalledExactlyOnceWith(path);
		expect(open).not.toHaveBeenCalled();
	});

	it("mounts a caller supplied preview only after a session link opens", async () => {
		const preview = vi.fn(() => <div data-testid="session-preview">Worker details</div>);
		render(<AppLink href="ao://sessions/project/session" hoverPreview={preview}>Worker</AppLink>);

		expect(preview).not.toHaveBeenCalled();
		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });
		await waitFor(() => expect(screen.getByTestId("session-preview")).toBeInTheDocument());
		expect(preview).toHaveBeenCalled();
	});
});
