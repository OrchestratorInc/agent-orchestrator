import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "./api-client";
import { splitBeside } from "./pane-split";
import { describePaneLayout, NOT_SHOWN, PaneCommandError, registerPaneSession, reportPaneLayout, type PaneLayout } from "./pane-relay";

class FakeSocket {
	static instances: FakeSocket[] = [];
	onopen: (() => void) | null = null;
	onclose: (() => void) | null = null;
	onmessage: ((event: MessageEvent) => void) | null = null;
	send = vi.fn();
	close = vi.fn();
	constructor(public url: string) {
		FakeSocket.instances.push(this);
	}
	command(command: Record<string, unknown>) {
		this.onmessage?.(new MessageEvent("message", { data: JSON.stringify({ ch: "panes", type: "command", command }) }));
	}
	sent(): Record<string, unknown>[] {
		return this.send.mock.calls.map(([frame]) => JSON.parse(String(frame)) as Record<string, unknown>);
	}
}

const layout: PaneLayout = { enabled: true, selected: "session", tabs: [{ key: "session", kind: "session", title: "Fix login" }], groups: [] };
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("pane relay", () => {
	const unregister: (() => void)[] = [];
	beforeEach(() => {
		FakeSocket.instances = [];
		vi.stubGlobal("WebSocket", FakeSocket);
		setApiBaseUrl("http://127.0.0.1:3001");
	});
	afterEach(() => {
		for (const done of unregister.splice(0)) done();
		setApiBaseUrl(null);
		vi.unstubAllGlobals();
	});

	it("subscribes, reports layouts and answers a command with the new layout", async () => {
		const handler = vi.fn(async () => ({ ...layout, selected: "sh-1" }));
		unregister.push(registerPaneSession("s-1", handler));
		reportPaneLayout("s-1", layout);
		const socket = FakeSocket.instances[0]!;
		socket.onopen?.();
		expect(socket.sent()).toEqual([
			{ ch: "panes", type: "subscribe" },
			{ ch: "panes", type: "layout", sessionId: "s-1", layout },
		]);

		socket.command({ id: "pane-1", sessionId: "s-1", action: "split", args: { action: "split", tab: "terminal", side: "bottom" } });
		await flush();
		expect(handler).toHaveBeenCalledWith({ action: "split", tab: "terminal", side: "bottom" });
		expect(socket.sent().at(-1)).toEqual({ ch: "panes", type: "result", result: { id: "pane-1", ok: true, layout: { ...layout, selected: "sh-1" } } });
	});

	it("says a session is not on screen, and passes on a handler's refusal", async () => {
		unregister.push(registerPaneSession("s-1", async () => {
			throw new PaneCommandError("SPLIT_PANES_OFF", "Split panes is off.");
		}));
		const socket = FakeSocket.instances[0]!;
		socket.onopen?.();

		socket.command({ id: "pane-1", sessionId: "other", action: "ungroup" });
		await flush();
		expect(socket.sent().at(-1)).toMatchObject({ type: "result", result: { id: "pane-1", ok: false, code: NOT_SHOWN } });

		socket.command({ id: "pane-2", sessionId: "s-1", action: "ungroup" });
		await flush();
		expect(socket.sent().at(-1)).toMatchObject({ type: "result", result: { id: "pane-2", ok: false, code: "SPLIT_PANES_OFF", message: "Split panes is off." } });
	});

	it("withdraws a session's layout when its view goes and closes with the last one", () => {
		const done = registerPaneSession("s-1", async () => layout);
		const socket = FakeSocket.instances[0]!;
		socket.onopen?.();
		done();
		expect(socket.sent().at(-1)).toEqual({ ch: "panes", type: "layout", sessionId: "s-1", layout: null });
		expect(socket.close).toHaveBeenCalled();
	});
});

describe("describePaneLayout", () => {
	it("lists each split's panes as fractions of the center area", () => {
		const groups = splitBeside([], "session", "sh-1", "right");
		const described = describePaneLayout({ enabled: true, selected: "sh-1", shownGroupId: groups[0]!.id, tabs: layout.tabs, groups });
		expect(described.shown).toBe(groups[0]!.id);
		expect(described.groups[0]).toEqual({
			id: groups[0]!.id,
			focus: "sh-1",
			panes: [
				{ key: "session", x: 0, y: 0, width: 0.5, height: 1 },
				{ key: "sh-1", x: 0.5, y: 0, width: 0.5, height: 1 },
			],
		});
	});
});
