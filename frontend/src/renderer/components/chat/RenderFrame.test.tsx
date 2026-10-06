import { act, render as rtlRender, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";
import { ActivityRow } from "./ChatTimelineItems";
import { ChatImageSourceProvider } from "./chat-image-source";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

function renderActivity(height = 300): ConversationActivity {
	return {
		kind: "activity",
		id: "act-1",
		sequence: 7,
		revision: 0,
		activityKind: "system",
		status: "completed",
		summary: "Turns by day",
		createdAt: "2026-10-06T10:00:00Z",
		detail: { event: "render", render: { id: "r1", title: "Turns by day", height, path: "/api/v1/sessions/proj-1/renders/r1" } },
	};
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

	it("expands the page at its inline width, centered in the dialog", async () => {
		const user = userEvent.setup();
		const width = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(660);
		try {
			render(<ActivityRow activity={renderActivity()} />);
			const inline = frame();
			await user.click(screen.getByRole("button", { name: "Expand page" }));
			const frames = await screen.findAllByTitle("Turns by day");
			expect(frames).toHaveLength(2);
			const expanded = frames.find((f) => f !== inline) as HTMLIFrameElement;
			expect(expanded.style.width).toBe("660px");
			expect(expanded.className).toContain("max-w-full");
			expect(expanded.parentElement?.className).toContain("justify-center");
			expect(inline.isConnected).toBe(true);
			expect(inline.style.width).toBe("");
			expect(inline.style.height).toBe("300px");
		} finally {
			width.mockRestore();
		}
	});

	it("falls back to a readable width when the inline box was not measured", async () => {
		const user = userEvent.setup();
		render(<ActivityRow activity={renderActivity()} />);
		await user.click(screen.getByRole("button", { name: "Expand page" }));
		const frames = await screen.findAllByTitle("Turns by day");
		const expanded = frames[1] as HTMLIFrameElement;
		expect(expanded.style.width).toBe("");
		expect(expanded.className).toContain("max-w-3xl");
	});
});
