import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import type { ConversationActivity } from "../../types/conversation";
import type { SessionArtifact } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";
import { ChatLinkProvider } from "./ChatMarkdown";
import { ActivityRow } from "./ChatTimelineItems";
import { ChatImageSourceProvider } from "./chat-image-source";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

function renderActivity(height = 300, heights?: Array<[number, number]>): ConversationActivity {
	return {
		kind: "activity",
		id: "act-1",
		sequence: 7,
		revision: 0,
		activityKind: "system",
		status: "completed",
		summary: "Turns by day",
		createdAt: "2026-10-06T10:00:00Z",
		detail: { event: "render", render: { id: "r1", title: "Turns by day", height, path: "/api/v1/sessions/proj-1/renders/r1", heights } },
	};
}

function fragmentOf(f: HTMLIFrameElement): { displayMode?: string } {
	return JSON.parse(decodeURIComponent((f.getAttribute("src") ?? "").split("#ao-theme=")[1] ?? "{}"));
}

function frame() {
	return screen.getByTitle("Turns by day") as HTMLIFrameElement;
}

function post(data: unknown, source: MessageEventSource | null) {
	act(() => {
		window.dispatchEvent(new MessageEvent("message", { data, source }));
	});
}

describe("render activity", () => {
	beforeEach(() => setApiBaseUrl("http://127.0.0.1:3001"));
	afterEach(() => {
		setApiBaseUrl(null);
		document.documentElement.removeAttribute("data-theme");
		document.documentElement.removeAttribute("data-style-theme");
	});

	it("frames the page sandboxed, from the daemon, with the theme in the fragment", () => {
		render(<ActivityRow activity={renderActivity()} />);
		expect(frame().getAttribute("sandbox")).toBe("allow-scripts allow-forms");
		expect(frame().getAttribute("src")).toMatch(/^http:\/\/127\.0\.0\.1:3001\/api\/v1\/sessions\/proj-1\/renders\/r1#ao-theme=/);
		expect(frame().style.height).toBe("300px");
	});

	it("follows the daemon to a new port, so a frame never points at a dead one", () => {
		render(<ActivityRow activity={renderActivity()} />);
		act(() => setApiBaseUrl("http://127.0.0.1:3555"));
		expect(frame().getAttribute("src")).toMatch(/^http:\/\/127\.0\.0\.1:3555\/api\/v1\/sessions\/proj-1\/renders\/r1#ao-theme=/);
	});

	it("opens an image the reader clicks in the page at full size, only from the frame in use", async () => {
		render(<ActivityRow activity={renderActivity()} />);
		const src = "data:image/png;base64,iVBORw0KGgo=";
		const request = { jsonrpc: "2.0", method: "ao/open-image", params: { src, alt: "Before" } };
		post(request, frame().contentWindow);
		expect(screen.queryByRole("dialog")).toBeNull();
		act(() => frame().focus());
		post(request, frame().contentWindow);
		const dialog = await screen.findByRole("dialog", { name: "Before" });
		expect(within(dialog).getByRole("img", { name: "Before" })).toHaveAttribute("src", src);
	});

	it("toggles reflow without reloading and shares the choice with the expanded page", async () => {
		const user = userEvent.setup();
		render(<ActivityRow activity={renderActivity()} />);
		const inline = frame();
		const src = inline.getAttribute("src");
		const sent = vi.spyOn(inline.contentWindow!, "postMessage");
		const wrap = screen.getByRole("button", { name: "Wrap content" });
		expect(wrap).toHaveAttribute("aria-pressed", "false");
		await user.click(wrap);
		expect(wrap).toHaveAttribute("aria-pressed", "true");
		expect(sent).toHaveBeenLastCalledWith(expect.objectContaining({ params: expect.objectContaining({ wrapContent: true }) }), "*");
		expect(inline.getAttribute("src")).toBe(src);

		await user.click(screen.getByRole("button", { name: "Expand page" }));
		const dialog = await screen.findByRole("dialog");
		const expanded = within(dialog).getByTitle("Turns by day") as HTMLIFrameElement;
		expect(fragmentOf(expanded)).toMatchObject({ wrapContent: true });
		const dialogWrap = within(dialog).getByRole("button", { name: "Wrap content" });
		expect(dialogWrap).toHaveAttribute("aria-pressed", "true");
		await user.click(dialogWrap);
		expect(sent).toHaveBeenLastCalledWith(expect.objectContaining({ params: expect.objectContaining({ wrapContent: false }) }), "*");
		await user.click(within(dialog).getByRole("button", { name: "Close" }));
		expect(wrap).toHaveAttribute("aria-pressed", "false");
		expect(inline.getAttribute("src")).toBe(src);
	});

	describe("in a remote host's chat", () => {
		const renderOrigin = "http://ao-preview-0123.localhost:4000";
		afterEach(() => vi.restoreAllMocks());

		function remoteChat(baseUrl?: string) {
			return render(
				<ChatImageSourceProvider sessionId="proj-1" assetBaseUrl={baseUrl} remoteHost hostId="h_box">
					<ActivityRow activity={renderActivity()} />
				</ChatImageSourceProvider>,
			);
		}

		it("frames the page from the proxy's render origin, sandboxed, without the proxy token", async () => {
			const previewUrl = vi.spyOn(aoBridge.remotes, "previewUrl").mockResolvedValue(`${renderOrigin}/r1`);
			remoteChat("http://127.0.0.1:4000/tok123");
			await waitFor(() => expect(frame().getAttribute("src")).toMatch(/^http:\/\/ao-preview-0123\.localhost:4000\/r1#ao-theme=/));
			expect(previewUrl).toHaveBeenCalledWith("h_box", "proj-1", "/api/v1/sessions/proj-1/renders/r1");
			expect(frame().getAttribute("sandbox")).toBe("allow-scripts allow-forms");
		});

		it("reads the source from the render origin and offers no local-only actions", async () => {
			vi.spyOn(aoBridge.remotes, "previewUrl").mockResolvedValue(`${renderOrigin}/r1`);
			const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<p>chart</p>"));
			const user = userEvent.setup();
			remoteChat("http://127.0.0.1:4000/tok123");
			await waitFor(() => expect(frame().getAttribute("src")).toContain(renderOrigin));
			await user.click(screen.getByRole("button", { name: "Expand page" }));
			const dialog = screen.getByRole("dialog");
			for (const name of ["Save as artifact", "Open in Orchestrator Browser", "Open in external browser"]) {
				expect(within(dialog).queryByRole("button", { name })).toBeNull();
			}
			await user.click(within(dialog).getByRole("button", { name: "View source" }));
			await waitFor(() => expect(fetch).toHaveBeenCalledWith(`${renderOrigin}/r1?source=1`, expect.anything()));
		});

		it("drops the previous origin while a reconnected host's lookup is pending", async () => {
			let resolveNext!: (url: string) => void;
			vi.spyOn(aoBridge.remotes, "previewUrl")
				.mockResolvedValueOnce(`${renderOrigin}/r1`)
				.mockReturnValueOnce(new Promise((resolve) => (resolveNext = resolve)));
			const chat = (baseUrl: string) => (
				<TooltipProvider>
					<ChatImageSourceProvider sessionId="proj-1" assetBaseUrl={baseUrl} remoteHost hostId="h_box">
						<ActivityRow activity={renderActivity()} />
					</ChatImageSourceProvider>
				</TooltipProvider>
			);
			const { rerender } = rtlRender(chat("http://127.0.0.1:4000/tok123"));
			await waitFor(() => expect(frame().getAttribute("src")).toContain(renderOrigin));
			rerender(chat("http://127.0.0.1:4001/tok456"));
			expect(document.querySelector("iframe")).toBeNull();
			act(() => resolveNext("http://ao-preview-4567.localhost:4001/r1"));
			await waitFor(() => expect(frame().getAttribute("src")).toMatch(/^http:\/\/ao-preview-4567\.localhost:4001\/r1#/));
		});

		it("shows the note while the host is not connected", () => {
			const previewUrl = vi.spyOn(aoBridge.remotes, "previewUrl");
			remoteChat();
			expect(screen.getByText(/Turns by day/)).toHaveTextContent("Turns by day · Open this session on its host to see the page.");
			expect(previewUrl).not.toHaveBeenCalled();
			expect(document.querySelector("iframe")).toBeNull();
		});
	});

	it("frames the page in a local chat", () => {
		render(
			<ChatImageSourceProvider sessionId="proj-1">
				<ActivityRow activity={renderActivity()} />
			</ChatImageSourceProvider>,
		);
		expect(frame().tagName).toBe("IFRAME");
	});

	it("fits the page's reported height, clamped, and ignores other windows", () => {
		render(<ActivityRow activity={renderActivity()} />);
		const size = (height: number) => ({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height } });
		post(size(640), frame().contentWindow);
		expect(frame().style.height).toBe("640px");
		post(size(120), window);
		expect(frame().style.height).toBe("640px");
		post(size(9000), frame().contentWindow);
		expect(frame().style.height).toBe("2000px");
	});

	it("opens at the height measured for the frame's width, follows the width, and fits the page once it reports", () => {
		const resized: ResizeObserverCallback[] = [];
		const disconnect = vi.fn();
		vi.spyOn(window, "ResizeObserver").mockImplementation(function (callback: ResizeObserverCallback) {
			resized.push(callback);
			return { observe() {}, unobserve() {}, disconnect };
		});
		const rect = vi.spyOn(HTMLIFrameElement.prototype, "getBoundingClientRect").mockReturnValue({ width: 640 } as DOMRect);
		try {
			const heights: Array<[number, number]> = [
				[320, 900],
				[640, 450],
				[1144, 300],
			];
			const { unmount } = render(<ActivityRow activity={renderActivity(300, heights)} />);
			expect(frame().style.height).toBe("450px");
			act(() => {
				for (const callback of resized) callback([{ contentRect: { width: 320 } }] as never, {} as ResizeObserver);
			});
			expect(frame().style.height).toBe("900px");
			post({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 512 } }, frame().contentWindow);
			expect(frame().style.height).toBe("512px");
			expect(disconnect).not.toHaveBeenCalled();
			unmount();
			expect(disconnect).toHaveBeenCalledTimes(1);
		} finally {
			rect.mockRestore();
			vi.mocked(window.ResizeObserver).mockRestore();
		}
	});

	it("restyles on a theme flip without reloading the page", async () => {
		render(<ActivityRow activity={renderActivity()} />);
		const src = frame().getAttribute("src");
		const sent: unknown[] = [];
		Object.defineProperty(frame().contentWindow!, "postMessage", {
			configurable: true,
			value: (message: unknown) => sent.push(message),
		});
		act(() => document.documentElement.setAttribute("data-theme", "light"));
		await waitFor(() =>
			expect(sent).toContainEqual(
				expect.objectContaining({ method: "ui/notifications/host-context-changed", params: expect.objectContaining({ theme: "light" }) }),
			),
		);
		expect(frame().getAttribute("src")).toBe(src);
	});

	it("restyles when the style theme changes, without reloading the page", async () => {
		const sheet = document.createElement("style");
		sheet.textContent = 'html[data-style-theme="dracula"] { --color-bg-primary: rgb(40,42,54); }';
		document.head.append(sheet);
		try {
			render(<ActivityRow activity={renderActivity()} />);
			const src = frame().getAttribute("src");
			const sent: unknown[] = [];
			Object.defineProperty(frame().contentWindow!, "postMessage", {
				configurable: true,
				value: (message: unknown) => sent.push(message),
			});
			act(() => document.documentElement.setAttribute("data-style-theme", "dracula"));
			await waitFor(() =>
				expect(sent).toContainEqual(
					expect.objectContaining({
						method: "ui/notifications/host-context-changed",
						params: expect.objectContaining({
							styles: { variables: expect.objectContaining({ "--background": "rgb(40,42,54)" }) },
						}),
					}),
				),
			);
			expect(frame().getAttribute("src")).toBe(src);
		} finally {
			sheet.remove();
		}
	});

	it("does not re-read the theme when only the root style attribute changes", async () => {
		render(<ActivityRow activity={renderActivity()} />);
		const read = vi.spyOn(window, "getComputedStyle");
		try {
			act(() => document.documentElement.style.setProperty("--sidebar-chrome-width", "240px"));
			await new Promise((resolve) => setTimeout(resolve, 50));
			expect(read).not.toHaveBeenCalled();
		} finally {
			read.mockRestore();
			document.documentElement.style.cssText = "";
		}
	});

	it("expands the page into a borderless dialog sized to it, in fullscreen display mode", async () => {
		const user = userEvent.setup();
		render(<ActivityRow activity={renderActivity()} />);
		const inline = frame();
		await user.click(screen.getByRole("button", { name: "Expand page" }));
		const frames = await screen.findAllByTitle("Turns by day");
		expect(frames).toHaveLength(2);
		const expanded = frames.find((f) => f !== inline) as HTMLIFrameElement;
		// The dialog is a reading column wide, with no border and no fixed height;
		// the frame fits the page, up to the window, and the page centers itself.
		const dialog = expanded.parentElement!;
		expect(dialog.getAttribute("role")).toBe("dialog");
		expect(dialog.className).toContain("w-[min(64rem,calc(100vw-4rem))]");
		expect(dialog.className).toContain("border-0");
		expect(dialog.className).not.toMatch(/(^|\s)(border|h-\S+)(\s|$)/);
		expect(expanded.className).toContain("w-full");
		expect(expanded.className).toContain("max-h-[calc(100svh-8rem)]");
		expect(expanded.style.width).toBe("");
		expect(expanded.style.height).toBe("300px");
		post({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 1200 } }, expanded.contentWindow);
		expect(expanded.style.height).toBe("1200px");
		expect(fragmentOf(expanded).displayMode).toBe("fullscreen");
		expect(fragmentOf(inline).displayMode).toBe("inline");
		expect(inline.isConnected).toBe(true);
		expect(inline.style.height).toBe("300px");
	});

	it("tells each frame its display mode with every theme change", async () => {
		const user = userEvent.setup();
		render(<ActivityRow activity={renderActivity()} />);
		const inline = frame();
		await user.click(screen.getByRole("button", { name: "Expand page" }));
		const expanded = (await screen.findAllByTitle("Turns by day")).find((f) => f !== inline) as HTMLIFrameElement;
		const sent = new Map<HTMLIFrameElement, unknown[]>([
			[inline, []],
			[expanded, []],
		]);
		for (const [f, messages] of sent) {
			Object.defineProperty(f.contentWindow!, "postMessage", { configurable: true, value: (m: unknown) => messages.push(m) });
		}
		act(() => document.documentElement.setAttribute("data-theme", "light"));
		const changed = (displayMode: string) =>
			expect.objectContaining({
				method: "ui/notifications/host-context-changed",
				params: expect.objectContaining({ theme: "light", displayMode }),
			});
		await waitFor(() => {
			expect(sent.get(inline)).toContainEqual(changed("inline"));
			expect(sent.get(expanded)).toContainEqual(changed("fullscreen"));
		});
	});
	async function expand() {
		const user = userEvent.setup();
		render(<ActivityRow activity={renderActivity()} />);
		await user.click(screen.getByRole("button", { name: "Expand page" }));
		return { user, dialog: await screen.findByRole("dialog") };
	}

	it("heads the expanded page with its title and the source, save and browser actions", async () => {
		const { dialog } = await expand();
		expect(within(dialog).getByRole("heading", { name: "Turns by day" })).toBeInTheDocument();
		for (const name of ["View source", "Save page", "Save as artifact", "Open in external browser", "Close"]) {
			expect(within(dialog).getByRole("button", { name })).toBeInTheDocument();
		}
		expect(within(dialog).getByRole("button", { name: "View source" })).toHaveAttribute("aria-pressed", "false");
		// Focus starts on the dialog itself, so no action's tooltip opens with it.
		expect(document.activeElement).toBe(dialog);
		expect(screen.queryByRole("tooltip")).toBeNull();
	});

	it("swaps the page for its source, as plain text, and back", async () => {
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<p>chart</p>\n<script>draw()</script>"));
		try {
			const { user, dialog } = await expand();
			await user.click(within(dialog).getByRole("button", { name: "View source" }));
			const source = await within(dialog).findByText(/<p>chart<\/p>/);
			expect(source.tagName).toBe("PRE");
			expect(source.textContent).toBe("<p>chart</p>\n<script>draw()</script>");
			expect(fetch).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/proj-1/renders/r1?source=1", expect.anything());
			expect(screen.getAllByTitle("Turns by day")).toHaveLength(1);
			const toggle = within(dialog).getByRole("button", { name: "View source" });
			expect(toggle).toHaveAttribute("aria-pressed", "true");
			await user.click(toggle);
			expect(screen.getAllByTitle("Turns by day")).toHaveLength(2);
			expect(within(dialog).queryByText(/<p>chart<\/p>/)).toBeNull();
		} finally {
			fetch.mockRestore();
		}
	});

	it("says so when the source cannot be fetched", async () => {
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 404 }));
		try {
			const { user, dialog } = await expand();
			await user.click(within(dialog).getByRole("button", { name: "View source" }));
			expect(await within(dialog).findByRole("alert")).toHaveTextContent("Could not load the page source.");
		} finally {
			fetch.mockRestore();
		}
	});

	it("saves the served page under its title", async () => {
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<html>page</html>", { headers: { "Content-Type": "text/html" } }));
		const original = { createObjectURL: URL.createObjectURL, revokeObjectURL: URL.revokeObjectURL };
		const created = vi.fn((_blob: Blob) => "blob:render");
		const revoked = vi.fn();
		Object.assign(URL, { createObjectURL: created, revokeObjectURL: revoked });
		const clicked: HTMLAnchorElement[] = [];
		const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
			clicked.push(this);
		});
		try {
			const { user, dialog } = await expand();
			await user.click(within(dialog).getByRole("button", { name: "Save page" }));
			await waitFor(() => expect(revoked).toHaveBeenCalledWith("blob:render"));
			// The page as served, bootstrap included; no fragment.
			expect(fetch).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/proj-1/renders/r1", expect.anything());
			expect(await created.mock.calls[0]![0].text()).toBe("<html>page</html>");
			expect(clicked).toHaveLength(1);
			expect(clicked[0]!.download).toBe("Turns by day.html");
			expect(clicked[0]!.getAttribute("href")).toBe("blob:render");
		} finally {
			click.mockRestore();
			Object.assign(URL, original);
			fetch.mockRestore();
		}
	});

	it("says so when the page cannot be saved, and takes one save at a time", async () => {
		let respond!: (response: Response) => void;
		const fetch = vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise((resolve) => (respond = resolve)));
		const logged = vi.spyOn(console, "error").mockImplementation(() => {});
		useUiStore.getState().clearGlobalToast();
		try {
			const { user, dialog } = await expand();
			const save = within(dialog).getByRole("button", { name: "Save page" });
			await user.click(save);
			expect(save).toBeDisabled();
			await act(async () => respond(new Response("{}", { status: 500 })));
			await waitFor(() => expect(save).toBeEnabled());
			expect(fetch).toHaveBeenCalledTimes(1);
			expect(useUiStore.getState().globalToasts).toEqual([
				expect.objectContaining({ title: "Could not save the page.", tone: "error" }),
			]);
			expect(logged).toHaveBeenCalledWith("save render", expect.any(Error));
		} finally {
			useUiStore.getState().clearGlobalToast();
			logged.mockRestore();
			fetch.mockRestore();
		}
	});

	it("keeps the page as a session artifact under its title", async () => {
		let respond!: (response: Response) => void;
		const fetch = vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise((resolve) => (respond = resolve)));
		useUiStore.getState().clearGlobalToast();
		try {
			const { user, dialog } = await expand();
			const keep = within(dialog).getByRole("button", { name: "Save as artifact" });
			await user.click(keep);
			expect(keep).toBeDisabled();
			expect(fetch).toHaveBeenCalledTimes(1);
			// The API client hands fetch a Request, or a rebased URL and init.
			const request = new Request(...(fetch.mock.calls[0]! as [RequestInfo | URL, RequestInit?]));
			expect(request.url).toBe("http://127.0.0.1:3001/api/v1/sessions/proj-1/renders/r1/artifact");
			expect(request.method).toBe("POST");
			expect(await request.json()).toEqual({ title: "Turns by day" });
			await act(async () =>
				respond(new Response(JSON.stringify({ path: "Turns by day.html", name: "Turns by day.html" }), { status: 201 })),
			);
			await waitFor(() => expect(keep).toBeEnabled());
			expect(useUiStore.getState().globalToasts).toEqual([
				expect.objectContaining({ title: "Saved to artifacts: Turns by day.html", tone: "info" }),
			]);
		} finally {
			useUiStore.getState().clearGlobalToast();
			fetch.mockRestore();
		}
	});

	it("says so when the page cannot be kept as an artifact", async () => {
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 404 }));
		const logged = vi.spyOn(console, "error").mockImplementation(() => {});
		useUiStore.getState().clearGlobalToast();
		try {
			const { user, dialog } = await expand();
			const keep = within(dialog).getByRole("button", { name: "Save as artifact" });
			await user.click(keep);
			await waitFor(() => expect(keep).toBeEnabled());
			expect(useUiStore.getState().globalToasts).toEqual([
				expect.objectContaining({ title: "Could not save the page to artifacts.", tone: "error" }),
			]);
			expect(logged).toHaveBeenCalledWith("save render as artifact", expect.any(Error));
		} finally {
			useUiStore.getState().clearGlobalToast();
			logged.mockRestore();
			fetch.mockRestore();
		}
	});

	it("opens the page in the browser, in fullscreen display mode", async () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		try {
			const { user, dialog } = await expand();
			await user.click(within(dialog).getByRole("button", { name: "Wrap content" }));
			await user.click(within(dialog).getByRole("button", { name: "Open in external browser" }));
			expect(open).toHaveBeenCalledTimes(1);
			const [url, target, features] = open.mock.calls[0]!;
			expect(url).toMatch(/^http:\/\/127\.0\.0\.1:3001\/api\/v1\/sessions\/proj-1\/renders\/r1#ao-theme=/);
			expect(JSON.parse(decodeURIComponent(String(url).split("#ao-theme=")[1]!))).toMatchObject({ displayMode: "fullscreen", wrapContent: true });
			expect([target, features]).toEqual(["_blank", "noopener,noreferrer"]);
		} finally {
			open.mockRestore();
		}
	});

	it("opens the page in the session's Browser panel straight from the inline frame", async () => {
		const user = userEvent.setup();
		const onLinkOpen = vi.fn();
		render(<ChatLinkProvider onLinkOpen={onLinkOpen}><ActivityRow activity={renderActivity()} /></ChatLinkProvider>);
		await user.click(screen.getByRole("button", { name: "Open in Orchestrator Browser" }));
		expect(onLinkOpen).toHaveBeenCalledExactlyOnceWith(expect.stringMatching(/^http:\/\/127\.0\.0\.1:3001\/api\/v1\/sessions\/proj-1\/renders\/r1#ao-theme=/));
	});
});

describe("artifact activity", () => {
	const url = "/api/v1/sessions/proj-1/artifact-files/q3/Q3%20%28final%29.html";
	const artifactActivity: ConversationActivity = {
		kind: "activity",
		id: "act-2",
		sequence: 8,
		revision: 0,
		activityKind: "system",
		status: "completed",
		summary: "Q3 (final).html",
		createdAt: "2026-10-08T10:00:00Z",
		detail: { event: "artifact", artifact: { path: "q3/Q3 (final).html", name: "Q3 (final).html", url } },
	};
	const previewUrl = "http://ao-preview-artifact.x.localhost:3001/q3/Q3%20(final).html";
	const inlineUrl = "http://ao-inline-artifact.x.localhost:3001/q3/Q3%20(final).html";
	const sessionArtifact = (path: string): SessionArtifact => ({ kind: "html", name: "Q3 (final).html", path, previewUrl, inlineUrl, size: 9, updatedAt: "2026-10-08T10:00:00Z" });
	// What a local session lists for the row's file.
	const listed = [sessionArtifact("q3/Q3 (final).html")];
	const openButton = () => screen.getByRole("button", { name: "Open Q3 (final).html in Artifacts tab" });

	function renderArtifact(artifacts: SessionArtifact[] | undefined = listed, hostId?: string) {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		return render(
			<QueryClientProvider client={client}>
				<ChatImageSourceProvider sessionId="proj-1" hostId={hostId} remoteHost={Boolean(hostId)} artifacts={artifacts}>
					<ActivityRow activity={artifactActivity} />
				</ChatImageSourceProvider>
			</QueryClientProvider>,
		);
	}

	beforeEach(() => setApiBaseUrl("http://127.0.0.1:3001"));
	afterEach(() => {
		setApiBaseUrl(null);
		useUiStore.setState({ inspectorSessions: {} });
	});

	it("names the artifact in one row and never frames its page in the thread", () => {
		renderArtifact();
		expect(document.querySelector("iframe")).toBeNull();
		const row = screen.getByTestId("reported-artifact-row");
		expect(row).toHaveTextContent("Artifact");
		expect(row).toHaveTextContent("Q3 (final).html");
	});

	it("opens the artifact in the Artifacts tab from the row", async () => {
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 200 }));
		try {
			const user = userEvent.setup();
			renderArtifact();
			await user.click(openButton());
			expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ sessionId: "proj-1", path: "q3/Q3 (final).html", source: "artifact" });
			expect(fetch).not.toHaveBeenCalled();
		} finally {
			fetch.mockRestore();
		}
	});

	it("opens the artifact on its remote host's session, so that session's view takes the request", async () => {
		const user = userEvent.setup();
		renderArtifact(listed, "https://lab.example:3001");
		expect(document.querySelector("iframe")).toBeNull();
		await user.click(openButton());
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({
			sessionId: "proj-1",
			hostId: "https://lab.example:3001",
			path: "q3/Q3 (final).html",
			source: "artifact",
		});
	});

	it("opens an old row's file where it was renamed to", async () => {
		const user = userEvent.setup();
		const renamed: SessionArtifact = { ...sessionArtifact("q3/final/report.html"), previousPaths: ["q3/Q3 (final).html"] };
		renderArtifact([sessionArtifact("other.html"), renamed]);
		await user.click(openButton());
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ sessionId: "proj-1", path: "q3/final/report.html", source: "artifact" });
	});

	it("prefers the file listed at the row's path over one renamed away from it", async () => {
		const user = userEvent.setup();
		const elsewhere: SessionArtifact = { ...sessionArtifact("old/report.html"), previousPaths: ["q3/Q3 (final).html"] };
		renderArtifact([elsewhere, sessionArtifact("q3/Q3 (final).html")]);
		await user.click(openButton());
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({ path: "q3/Q3 (final).html" });
	});

	it("shows the name without an action when the session does not list the artifact", () => {
		renderArtifact([sessionArtifact("other.html")]);
		expect(screen.getByTestId("reported-artifact-row")).toHaveTextContent("Q3 (final).html");
		expect(screen.queryByRole("button")).toBeNull();
		expect(document.querySelector("iframe")).toBeNull();
	});

	it("rereads the session's artifacts while a row's file is missing, and offers to open it once listed", () => {
		// The row arrives on the chat stream; the list can lag behind a file just reported.
		const opened: string[] = [];
		const closed: string[] = [];
		class EventSourceStub {
			constructor(readonly url: string) {
				opened.push(url);
			}
			addEventListener() {}
			close() {
				closed.push(this.url);
			}
		}
		vi.stubGlobal("EventSource", EventSourceStub);
		try {
			const client = new QueryClient();
			const view = (artifacts: SessionArtifact[]) => (
				<TooltipProvider>
					<QueryClientProvider client={client}>
						<ChatImageSourceProvider sessionId="proj-1" artifacts={artifacts}>
							<ActivityRow activity={artifactActivity} />
						</ChatImageSourceProvider>
					</QueryClientProvider>
				</TooltipProvider>
			);
			const { rerender } = rtlRender(view([]));
			expect(opened).toEqual(["http://127.0.0.1:3001/api/v1/sessions/proj-1/artifact-events"]);
			expect(screen.queryByRole("button")).toBeNull();
			rerender(view(listed));
			expect(openButton()).toBeInTheDocument();
			// Listed, the row stops following the directory.
			expect(closed).toEqual(opened);
		} finally {
			vi.unstubAllGlobals();
		}
	});

	it("does not follow a remote host's artifact directory from this computer", () => {
		const opened: string[] = [];
		vi.stubGlobal("EventSource", class {
			constructor(url: string) {
				opened.push(url);
			}
			addEventListener() {}
			close() {}
		});
		try {
			renderArtifact([], "https://lab.example:3001");
			expect(opened).toEqual([]);
		} finally {
			vi.unstubAllGlobals();
		}
	});
});
