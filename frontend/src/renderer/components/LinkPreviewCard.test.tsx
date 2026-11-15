import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppBrowserLinkContext, AppLink } from "./AppLink";
import { forgetSettledFavicons, LinkPreviewCard } from "./LinkPreviewCard";
import { aoBridge } from "../lib/bridge";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: () => "request failed",
}));

// The hook caches per URL in the shared module-level queryClient, so each test
// hovers a distinct URL to stay isolated.
const url = "https://example.com/docs/getting-started";

/** A 16x16 RGBA image with an opaque glyph on half its width. */
function glyph(r: number, g: number, b: number): Uint8ClampedArray {
	const pixels = new Uint8ClampedArray(16 * 16 * 4);
	for (let index = 0; index < pixels.length; index += 4) {
		if ((index / 4) % 16 < 8) pixels.set([r, g, b, 255], index);
	}
	return pixels;
}

afterEach(() => {
	getMock.mockReset();
	vi.restoreAllMocks();
});

describe("AppLink link preview", () => {
	it("opens the original URL from the preview title and domain using the chat browser context", async () => {
		const originalUrl = "https://click.example.com/original";
		const open = vi.fn();
		getMock.mockResolvedValue({ data: { url: "https://redirect.example.com/", title: "Clickable title" } });
		render(<AppBrowserLinkContext.Provider value={open}><AppLink href={originalUrl}>docs</AppLink></AppBrowserLinkContext.Provider>);
		fireEvent.pointerOver(screen.getByRole("link", { name: "docs" }), { pointerType: "mouse" });
		const title = await screen.findByRole("link", { name: "Clickable title" });
		expect(title).toHaveAttribute("href", originalUrl);
		fireEvent.click(title);
		fireEvent.click(screen.getByRole("link", { name: "redirect.example.com" }));
		expect(open.mock.calls).toEqual([[originalUrl], [originalUrl]]);
	});

	it.each([{}, { ctrlKey: true }, { metaKey: true }, { altKey: true }])("opens externally when no browser is available or modifiers are used: %j", (modifiers) => {
		const open = vi.fn();
		const external = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<LinkPreviewCard url={url} preview={{ url, title: "Open page" }} onBrowserOpen={Object.keys(modifiers).length ? open : undefined} />);
		fireEvent.click(screen.getByRole("link", { name: "Open page" }), modifiers);
		expect(external).toHaveBeenCalledExactlyOnceWith(url);
		expect(open).not.toHaveBeenCalled();
	});

	it("keeps the domain clickable when the preview has no title", () => {
		const open = vi.fn();
		render(<LinkPreviewCard url={url} preview={{ url }} onBrowserOpen={open} />);
		fireEvent.click(screen.getByRole("link", { name: "example.com" }));
		expect(open).toHaveBeenCalledExactlyOnceWith(url);
	});

	it("fetches and shows the preview card on hover", async () => {
		getMock.mockResolvedValue({
			data: {
				url,
				title: "Getting Started",
				description: "How to install and run the thing.",
				siteName: "Example Docs",
				faviconUrl: "https://example.com/favicon.ico",
				imageUrl: "https://example.com/og.png",
			},
			error: undefined,
		});
		render(<AppLink href={url}>docs</AppLink>);

		expect(getMock).not.toHaveBeenCalled();
		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });

		await waitFor(() => expect(screen.getByTestId("link-preview-card")).toBeInTheDocument());
		expect(getMock).toHaveBeenCalledExactlyOnceWith("/api/v1/link-preview", {
			params: { query: { url } },
			signal: expect.any(AbortSignal),
		});
		expect(screen.getByText("Getting Started")).toBeInTheDocument();
		expect(screen.getByText("How to install and run the thing.")).toBeInTheDocument();
		// The domain is shown rather than the page's self-reported site name.
		expect(screen.queryByText("Example Docs")).not.toBeInTheDocument();
		expect(screen.getByText("example.com")).toBeInTheDocument();
		expect(screen.getByTestId("link-preview-card").querySelectorAll("img")).toHaveLength(2);
	});

	it("falls back to the domain when no site name is returned", async () => {
		const fallbackUrl = "https://fallback.example.net/page";
		getMock.mockResolvedValue({ data: { url: fallbackUrl, title: "A page" }, error: undefined });
		render(<AppLink href={fallbackUrl}>docs</AppLink>);

		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });

		await waitFor(() => expect(screen.getByTestId("link-preview-card")).toBeInTheDocument());
		expect(screen.getAllByText("fallback.example.net").length).toBeGreaterThan(0);
		expect(screen.getByText("A page")).toBeInTheDocument();
	});

	it("settles a failed preview into a compact fallback that still opens the link", async () => {
		const brokenUrl = "https://broken.example.org/docs/page?tab=1";
		const open = vi.fn();
		getMock.mockResolvedValue({ data: undefined, error: { message: "upstream down" } });
		render(<AppBrowserLinkContext.Provider value={open}><AppLink href={brokenUrl}>docs</AppLink></AppBrowserLinkContext.Provider>);

		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });

		const fallback = await screen.findByTestId("link-preview-fallback");
		expect(screen.queryByTestId("link-preview-loading")).not.toBeInTheDocument();
		expect(fallback).toHaveTextContent("broken.example.org");
		expect(fallback).toHaveTextContent("/docs/page?tab=1");
		expect(screen.getByRole("status")).toHaveTextContent("No preview available");
		fireEvent.click(screen.getByRole("link", { name: "broken.example.org" }));
		expect(open).toHaveBeenCalledExactlyOnceWith(brokenUrl);
	});

	it("shows the destination while loading rather than only a skeleton", async () => {
		const pendingUrl = "https://pending.example.org/page";
		getMock.mockImplementation(() => new Promise(() => undefined));
		render(<AppLink href={pendingUrl}>docs</AppLink>);

		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });

		expect(await screen.findByTestId("link-preview-loading")).toHaveTextContent("pending.example.org");
	});

	it("uses the fallback for a page with no metadata", () => {
		render(<LinkPreviewCard url={url} preview={{ url }} />);
		expect(screen.getByTestId("link-preview-fallback")).toBeInTheDocument();
		expect(screen.queryByTestId("link-preview-card")).not.toBeInTheDocument();
	});

	it("opens the preview from keyboard focus", async () => {
		const focusUrl = "https://keyboard.example.org/";
		getMock.mockResolvedValue({ data: { url: focusUrl, title: "Keyboard reached" } });
		render(<AppLink href={focusUrl}>docs</AppLink>);
		const link = screen.getByRole("link");
		// jsdom never reports :focus-visible; keyboard focus does in the browser.
		vi.spyOn(link, "matches").mockImplementation((selector) => selector === ":focus-visible");

		act(() => link.focus());

		expect(await screen.findByText("Keyboard reached")).toBeInTheDocument();
	});

	it("does not open the preview when focus returns to a clicked link", async () => {
		const clickedUrl = "https://clicked.example.org/";
		render(<AppLink href={clickedUrl}>docs</AppLink>);
		const link = screen.getByRole("link");
		vi.spyOn(link, "matches").mockReturnValue(false);

		act(() => link.focus());
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 400)); });

		expect(getMock).not.toHaveBeenCalled();
		expect(screen.queryByTestId("link-preview-loading")).not.toBeInTheDocument();
	});

	describe("favicon theme handling", () => {
		const faviconPreview = { url, title: "Page", faviconUrl: "https://example.com/favicon.svg" };
		const favicon = () => screen.getByTestId("link-preview-card").querySelector("img")!;
		// Settled icons are remembered per URL for the run; each case starts fresh.
		beforeEach(forgetSettledFavicons);

		it("loads with CORS and inverts a dark glyph in dark themes", () => {
			vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
				drawImage: vi.fn(),
				getImageData: () => ({ data: glyph(36, 41, 46) }),
			} as unknown as CanvasRenderingContext2D);
			render(<LinkPreviewCard url={url} preview={faviconPreview} />);
			expect(favicon()).toHaveAttribute("crossorigin", "anonymous");
			fireEvent.load(favicon());
			expect(favicon()).toHaveClass("dark:invert");
		});

		it("falls back to a plain load with a plate when the host refuses CORS", () => {
			render(<LinkPreviewCard url={url} preview={faviconPreview} />);
			fireEvent.error(favicon());
			expect(favicon()).not.toHaveAttribute("crossorigin");
			// Lazy loading left the retry unstarted in an open card, showing a blank icon.
			expect(favicon()).not.toHaveAttribute("loading", "lazy");
			fireEvent.load(favicon());
			expect(favicon()).toHaveClass("dark:bg-white/90");
		});

		it("shows the generic icon when the favicon cannot load at all", () => {
			render(<LinkPreviewCard url={url} preview={faviconPreview} />);
			fireEvent.error(favicon());
			fireEvent.error(favicon());
			expect(screen.getByTestId("link-preview-card").querySelector("img")).toBeNull();
		});

		it("does not carry an icon's failure to the next link in a reused card", () => {
			const { rerender } = render(<LinkPreviewCard url={url} preview={faviconPreview} />);
			fireEvent.error(favicon());
			fireEvent.error(favicon());
			rerender(<LinkPreviewCard url={url} preview={{ ...faviconPreview, faviconUrl: "https://other.example/favicon.svg" }} />);
			expect(favicon()).toHaveAttribute("src", "https://other.example/favicon.svg");
			expect(favicon()).toHaveAttribute("crossorigin", "anonymous");
		});
	});

	it("does not fetch previews for non-web links", async () => {
		render(<AppLink href="mailto:dev@example.com">mail</AppLink>);

		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });

		await new Promise((resolve) => setTimeout(resolve, 400));
		expect(getMock).not.toHaveBeenCalled();
		expect(screen.queryByTestId("link-preview-card")).not.toBeInTheDocument();
	});

	it("keeps the context menu working while the hover card is wired", async () => {
		const menuUrl = "https://menu.example.io/item";
		getMock.mockResolvedValue({ data: { url: menuUrl }, error: undefined });
		render(<AppLink href={menuUrl}>docs</AppLink>);

		fireEvent.contextMenu(screen.getByRole("link"));

		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Open in Orchestrator Browser", "Open in external browser", "Copy link",
		]);
	});
});
