import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import type { ConversationActivity } from "../../types/conversation";
import type { SessionArtifact } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";
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

	it("shows a note instead of a frame in a remote host's chat", () => {
		// The local daemon has no copy of a remote host's render, and the remote
		// proxy URL carries a capability token the page could read.
		render(
			<ChatImageSourceProvider sessionId="proj-1" remoteHost>
				<ActivityRow activity={renderActivity()} />
			</ChatImageSourceProvider>,
		);
		expect(document.querySelector("iframe")).toBeNull();
		expect(screen.getByText(/Turns by day/)).toHaveTextContent("Turns by day · Open this session on its host to see the page.");
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
			await user.click(within(dialog).getByRole("button", { name: "Open in external browser" }));
			expect(open).toHaveBeenCalledTimes(1);
			const [url, target, features] = open.mock.calls[0]!;
			expect(url).toMatch(/^http:\/\/127\.0\.0\.1:3001\/api\/v1\/sessions\/proj-1\/renders\/r1#ao-theme=/);
			expect(JSON.parse(decodeURIComponent(String(url).split("#ao-theme=")[1]!)).displayMode).toBe("fullscreen");
			expect([target, features]).toEqual(["_blank", "noopener,noreferrer"]);
		} finally {
			open.mockRestore();
		}
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
	const sessionArtifact = (path: string): SessionArtifact => ({ kind: "html", name: "Q3 (final).html", path, previewUrl, size: 9, updatedAt: "2026-10-08T10:00:00Z" });

	function renderArtifact(artifacts?: SessionArtifact[]) {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		return render(
			<QueryClientProvider client={client}>
				<ChatImageSourceProvider sessionId="proj-1" artifacts={artifacts}>
					<ActivityRow activity={artifactActivity} />
				</ChatImageSourceProvider>
			</QueryClientProvider>,
		);
	}

	async function expandArtifact(artifacts?: SessionArtifact[]) {
		const user = userEvent.setup();
		renderArtifact(artifacts);
		await user.click(screen.getByRole("button", { name: "Expand page" }));
		return { user, dialog: await screen.findByRole("dialog") };
	}

	const artifactFrame = () => screen.getByTitle("Q3 (final).html") as HTMLIFrameElement;

	it("frames the artifact from its own inline origin, same-origin there, when the session lists one", () => {
		const inlineUrl = "http://ao-inline-artifact.x.localhost:3001/q3/Q3%20(final).html";
		renderArtifact([{ ...sessionArtifact("q3/Q3 (final).html"), inlineUrl }]);
		expect(artifactFrame().getAttribute("src")).toMatch(/^http:\/\/ao-inline-artifact\.x\.localhost:3001\/q3\/Q3%20\(final\)\.html#ao-theme=/);
		expect(artifactFrame().getAttribute("sandbox")).toBe("allow-scripts allow-forms allow-same-origin");
	});

	it("never frames an inline URL off the inline-artifact origin with allow-same-origin", () => {
		// allow-same-origin on the app's own origin would let the page script the app.
		for (const inlineUrl of ["http://localhost:5173/q3/x.html", "app://renderer/x.html", "http://ao-inline-artifact.x.example/x.html"]) {
			const { unmount } = renderArtifact([{ ...sessionArtifact("q3/Q3 (final).html"), inlineUrl }]);
			expect(artifactFrame().getAttribute("sandbox")).toBe("allow-scripts allow-forms");
			expect(artifactFrame().getAttribute("src")).toMatch(/^http:\/\/127\.0\.0\.1:3001\/api\/v1\/sessions\/proj-1\/artifact-files\//);
			unmount();
		}
	});

	beforeEach(() => setApiBaseUrl("http://127.0.0.1:3001"));
	afterEach(() => {
		setApiBaseUrl(null);
		useUiStore.setState({ inspectorSessions: {} });
	});

	it("frames the artifact sandboxed, from the artifact route, at 400 until the page reports its height", () => {
		renderArtifact();
		expect(artifactFrame().getAttribute("sandbox")).toBe("allow-scripts allow-forms");
		expect(artifactFrame().getAttribute("src")).toMatch(
			/^http:\/\/127\.0\.0\.1:3001\/api\/v1\/sessions\/proj-1\/artifact-files\/q3\/Q3%20%28final%29\.html#ao-theme=/,
		);
		expect(artifactFrame().style.height).toBe("400px");
		post({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 520 } }, artifactFrame().contentWindow);
		expect(artifactFrame().style.height).toBe("520px");
	});

	it("caps the inline artifact at 640 and lets the page scroll inside it, so a page sized to its viewport cannot ratchet the frame", () => {
		renderArtifact();
		const fragment = JSON.parse(decodeURIComponent(artifactFrame().getAttribute("src")!.split("#ao-theme=")[1]!));
		expect(fragment).toMatchObject({ displayMode: "inline", scrollable: true });
		// A min-height:100vh page reports the frame height plus its padding, again and again.
		for (const height of [448, 496, 688, 1_200, 2_000]) {
			post({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height } }, artifactFrame().contentWindow);
		}
		expect(artifactFrame().style.height).toBe("640px");
	});

	it("fits the expanded artifact past 640, leaving the window to cap it", async () => {
		const { dialog } = await expandArtifact();
		const expanded = within(dialog).getByTitle("Q3 (final).html") as HTMLIFrameElement;
		post({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 900 } }, expanded.contentWindow);
		expect(expanded.style.height).toBe("900px");
		expect(expanded.className).toContain("max-h-[calc(100svh-8rem)]");
	});

	it("shows the remote-host note instead of a frame", () => {
		render(
			<ChatImageSourceProvider sessionId="proj-1" remoteHost>
				<ActivityRow activity={artifactActivity} />
			</ChatImageSourceProvider>,
		);
		expect(document.querySelector("iframe")).toBeNull();
		expect(screen.getByText(/Q3 \(final\)\.html/)).toHaveTextContent("Q3 (final).html · Open this session on its host to see the page.");
	});

	it("heads the dialog with the artifact's name and has no Save as artifact", async () => {
		const { dialog } = await expandArtifact([sessionArtifact("other.html")]);
		expect(within(dialog).getByRole("heading", { name: "Q3 (final).html" })).toBeInTheDocument();
		for (const name of ["View source", "Save page", "Open in external browser", "Close"]) {
			expect(within(dialog).getByRole("button", { name })).toBeInTheDocument();
		}
		expect(within(dialog).queryByRole("button", { name: "Save as artifact" })).toBeNull();
		// The session lists no preview for this path.
		expect(within(dialog).queryByRole("button", { name: "Open in Browser panel" })).toBeNull();
	});

	it("opens the session's preview of the artifact in the Browser panel and closes the dialog", async () => {
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 200 }));
		try {
			const { user, dialog } = await expandArtifact([sessionArtifact("q3/Q3 (final).html")]);
			await user.click(within(dialog).getByRole("button", { name: "Open in Browser panel" }));
			expect(screen.queryByRole("dialog")).toBeNull();
			expect(useUiStore.getState().inspectorSessions["proj-1"]).toMatchObject({ isOpen: true, view: "browser" });
			await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
			const request = new Request(...(fetch.mock.calls[0]! as [RequestInfo | URL, RequestInit?]));
			expect(request.url).toBe("http://127.0.0.1:3001/api/v1/sessions/proj-1/preview");
			expect(request.method).toBe("POST");
			expect(await request.json()).toEqual({ url: previewUrl });
		} finally {
			fetch.mockRestore();
		}
	});

	it("reads the source and saves the served page under the artifact's name", async () => {
		const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () => new Response("<p>report</p>"));
		const original = { createObjectURL: URL.createObjectURL, revokeObjectURL: URL.revokeObjectURL };
		const revoked = vi.fn();
		Object.assign(URL, { createObjectURL: () => "blob:artifact", revokeObjectURL: revoked });
		const clicked: HTMLAnchorElement[] = [];
		const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
			clicked.push(this);
		});
		try {
			const { user, dialog } = await expandArtifact();
			await user.click(within(dialog).getByRole("button", { name: "View source" }));
			expect(await within(dialog).findByText("<p>report</p>")).toBeInTheDocument();
			expect(fetch).toHaveBeenCalledWith(`http://127.0.0.1:3001${url}?source=1`, expect.anything());
			await user.click(within(dialog).getByRole("button", { name: "Save page" }));
			await waitFor(() => expect(revoked).toHaveBeenCalledWith("blob:artifact"));
			expect(fetch).toHaveBeenCalledWith(`http://127.0.0.1:3001${url}`, expect.anything());
			expect(clicked.map((link) => link.download)).toEqual(["Q3 (final).html"]);
		} finally {
			click.mockRestore();
			Object.assign(URL, original);
			fetch.mockRestore();
		}
	});
});
