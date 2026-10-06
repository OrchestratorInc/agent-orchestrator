import { act, render as rtlRender, screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";
import { ActivityRow } from "./ChatTimelineItems";

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
	});

	it("frames the page sandboxed, from the daemon, with the theme in the fragment", () => {
		render(<ActivityRow activity={renderActivity()} />);
		expect(frame().getAttribute("sandbox")).toBe("allow-scripts allow-forms");
		expect(frame().getAttribute("src")).toMatch(/^http:\/\/127\.0\.0\.1:3001\/api\/v1\/sessions\/proj-1\/renders\/r1#ao-theme=/);
		expect(frame().style.height).toBe("300px");
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
});
