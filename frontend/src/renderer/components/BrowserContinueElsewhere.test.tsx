import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrowserSiteBlockRow, resetUserBrowserForTest } from "./BrowserContinueElsewhere";

const blocked = { reason: "blocked" as const, host: "shop.test" };

describe("BrowserSiteBlockRow", () => {
	beforeEach(() => resetUserBrowserForTest());
	afterEach(() => {
		vi.restoreAllMocks();
		vi.useRealTimers();
	});

	it("says why the page fails and offers the person's Chrome", async () => {
		vi.spyOn(window.ao!.browser, "userBrowser").mockResolvedValue({ name: "Chrome", chrome: true });
		render(<BrowserSiteBlockRow siteBlock={{ reason: "verification", host: "dash.cloudflare.com" }} viewId="v1" />);
		expect(screen.getByText("dash.cloudflare.com keeps asking to check you're human here.")).toBeInTheDocument();
		expect(await screen.findByRole("button", { name: "Continue in Chrome" })).toBeInTheDocument();
	});

	it("names the default browser, or no browser, when Chrome is not installed", async () => {
		vi.spyOn(window.ao!.browser, "userBrowser").mockResolvedValue(null);
		render(<BrowserSiteBlockRow siteBlock={{ reason: "signin", host: "accounts.google.com" }} viewId="v1" />);
		expect(screen.getByText("Google won't sign you in from this browser.")).toBeInTheDocument();
		expect(await screen.findByRole("button", { name: "Continue in your browser" })).toBeInTheDocument();
	});

	it("opens the page, confirms where, then gets out of the way", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		vi.spyOn(window.ao!.browser, "userBrowser").mockResolvedValue({ name: "Chrome", chrome: true });
		const open = vi.spyOn(window.ao!.browser, "openInUserBrowser").mockResolvedValue({ name: "Chrome", chrome: true });
		render(<BrowserSiteBlockRow siteBlock={blocked} viewId="v1" />);
		fireEvent.click(await screen.findByRole("button", { name: "Continue in Chrome" }));
		expect(await screen.findByText("Opened in Chrome")).toBeInTheDocument();
		expect(open).toHaveBeenCalledWith("v1");
		expect(screen.queryByRole("button", { name: "Continue in Chrome" })).not.toBeInTheDocument();
		await act(async () => {
			await vi.advanceTimersByTimeAsync(3_000);
		});
		await waitFor(() => expect(screen.queryByTestId("browser-site-block")).not.toBeInTheDocument());
	});

	it("keeps the way out when opening fails", async () => {
		vi.spyOn(window.ao!.browser, "userBrowser").mockResolvedValue({ name: "Chrome", chrome: true });
		vi.spyOn(window.ao!.browser, "openInUserBrowser").mockRejectedValue(new Error("no"));
		render(<BrowserSiteBlockRow siteBlock={blocked} viewId="v1" />);
		fireEvent.click(await screen.findByRole("button", { name: "Continue in Chrome" }));
		expect(await screen.findByText("Couldn't open the page")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Continue in Chrome" })).toBeEnabled();
	});

	it("stays dismissed for that site, and still offers for another", async () => {
		const { rerender } = render(<BrowserSiteBlockRow siteBlock={blocked} viewId="v1" />);
		fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
		await waitFor(() => expect(screen.queryByTestId("browser-site-block")).not.toBeInTheDocument());

		rerender(<BrowserSiteBlockRow siteBlock={undefined} viewId="v1" />);
		rerender(<BrowserSiteBlockRow siteBlock={blocked} viewId="v1" />);
		expect(screen.queryByTestId("browser-site-block")).not.toBeInTheDocument();

		rerender(<BrowserSiteBlockRow siteBlock={{ reason: "blocked", host: "other.test" }} viewId="v1" />);
		expect(await screen.findByText("other.test is blocking this browser.")).toBeInTheDocument();
	});
});
