import { describe, expect, it } from "vitest";
import { dragInAppWindow } from "./app-window-drag";

type Sent = { type: string; x: number; y: number; modifiers?: string[] };

function view(bounds: { x: number; y: number; width: number; height: number }, attach: () => void) {
	const sent: Sent[] = [];
	const cdp: Array<{ method: string; params: Record<string, unknown> }> = [];
	let attached = false;
	const webContents = {
		isDestroyed: () => false,
		getZoomFactor: () => 2,
		sendInputEvent: (event: Sent) => sent.push(event),
		debugger: {
			isAttached: () => attached,
			attach: () => {
				attach();
				attached = true;
			},
			detach: () => {
				attached = false;
			},
			on: () => {},
			removeListener: () => {},
			sendCommand: async (method: string, params: Record<string, unknown>) => {
				cdp.push({ method, params });
				return {};
			},
		},
	};
	return { sent, cdp, view: { getBounds: () => bounds, getVisible: () => true, webContents } };
}

function windowWith(children: unknown[]) {
	return {
		isDestroyed: () => false,
		getMediaSourceId: () => "window:4242:0",
		getBounds: () => ({ x: 100, y: 100, width: 800, height: 600 }),
		getContentBounds: () => ({ x: 100, y: 128, width: 800, height: 572 }),
		contentView: { children },
	};
}

const refuse = () => {
	throw new Error("DevTools is open");
};

describe("dragInAppWindow", () => {
	it("posts the drag to the topmost page under the start point, in view coordinates", async () => {
		const shell = view({ x: 0, y: 0, width: 800, height: 572 }, refuse);
		const panel = view({ x: 400, y: 0, width: 400, height: 572 }, refuse);
		const window = windowWith([shell.view, panel.view]);
		const result = await dragInAppWindow([window] as never, {
			windowId: 4242, fromX: 500, fromY: 128, toX: 600, toY: 228, durationMs: 0, steps: 2, modifiers: ["cmd"],
		});
		expect(result.route).toBe("send_input_event");
		expect(shell.sent).toEqual([]);
		expect(panel.sent.map(({ type, x, y }) => [type, x, y])).toEqual([
			["mouseMove", 100, 100], ["mouseDown", 100, 100], ["mouseMove", 150, 150], ["mouseMove", 200, 200], ["mouseUp", 200, 200],
		]);
		expect(panel.sent[1].modifiers).toEqual(["meta", "leftButtonDown"]);
		expect(panel.sent[4].modifiers).toEqual(["meta"]);
	});

	it("uses CDP in CSS pixels when it can attach, and detaches", async () => {
		const shell = view({ x: 0, y: 0, width: 800, height: 572 }, () => {});
		await dragInAppWindow([windowWith([shell.view])] as never, {
			windowId: 4242, fromX: 10, fromY: 48, toX: 30, toY: 68, durationMs: 0, steps: 1,
		});
		const mouse = shell.cdp.filter(({ method }) => method === "Input.dispatchMouseEvent").map(({ params }) => [params.type, params.x, params.y]);
		expect(mouse).toEqual([["mouseMoved", 5, 10], ["mousePressed", 5, 10], ["mouseMoved", 15, 20], ["mouseReleased", 15, 20]]);
		expect(shell.view.webContents.debugger.isAttached()).toBe(false);
	});

	it("refuses an unknown window and a drag outside the content", async () => {
		const window = windowWith([view({ x: 0, y: 0, width: 800, height: 572 }, refuse).view]);
		await expect(dragInAppWindow([window] as never, { windowId: 1, fromX: 1, fromY: 40, toX: 2, toY: 40 })).rejects.toMatchObject({ code: "APP_WINDOW_NOT_FOUND" });
		await expect(dragInAppWindow([window] as never, { windowId: 4242, fromX: 1, fromY: 10, toX: 2, toY: 40 })).rejects.toMatchObject({ code: "APP_DRAG_OUTSIDE" });
	});

	it("runs overlapping drags one after the other, release and detach included", async () => {
		const shell = view({ x: 0, y: 0, width: 800, height: 572 }, () => {});
		const window = windowWith([shell.view]);
		const drag = (fromX: number, signal?: AbortSignal) =>
			dragInAppWindow([window] as never, { windowId: 4242, fromX, fromY: 48, toX: fromX + 20, toY: 68, durationMs: 20, steps: 2 }, signal);
		const cancelled = new AbortController();
		const results = Promise.all([drag(10), drag(100), drag(200, cancelled.signal).catch((error) => error)]);
		cancelled.abort();
		const [first, second, third] = await results;
		expect([first.route, second.route]).toEqual(["cdp", "cdp"]);
		expect(third).toMatchObject({ code: "APP_DRAG_ABORTED" });
		const mouse = shell.cdp.filter(({ method }) => method === "Input.dispatchMouseEvent").map(({ params }) => `${params.type}@${params.x}`);
		expect(mouse).toEqual([
			"mouseMoved@5", "mousePressed@5", "mouseMoved@10", "mouseMoved@15", "mouseReleased@15",
			"mouseMoved@50", "mousePressed@50", "mouseMoved@55", "mouseMoved@60", "mouseReleased@60",
		]);
		expect(shell.sent).toEqual([]);
	});

	it("releases a cancelled drag where the pointer got to and reports it cancelled", async () => {
		const args = { windowId: 4242, fromX: 10, fromY: 48, toX: 30, toY: 68, durationMs: 0, steps: 2 };
		const cdp = view({ x: 0, y: 0, width: 800, height: 572 }, () => {});
		const cdpCancel = new AbortController();
		const sendCommand = cdp.view.webContents.debugger.sendCommand;
		cdp.view.webContents.debugger.sendCommand = async (method, params) => {
			if (params.type === "mouseMoved" && params.buttons === 1) cdpCancel.abort();
			return sendCommand(method, params);
		};
		await expect(dragInAppWindow([windowWith([cdp.view])] as never, args, cdpCancel.signal)).rejects.toMatchObject({ code: "APP_DRAG_ABORTED" });
		expect(cdp.cdp.filter(({ method }) => method === "Input.dispatchMouseEvent").map(({ params }) => `${params.type}@${params.x},${params.y}`))
			.toEqual(["mouseMoved@5,10", "mousePressed@5,10", "mouseMoved@10,15", "mouseReleased@10,15"]);

		const input = view({ x: 0, y: 0, width: 800, height: 572 }, refuse);
		const inputCancel = new AbortController();
		const sendInputEvent = input.view.webContents.sendInputEvent;
		input.view.webContents.sendInputEvent = (event) => {
			if (event.type === "mouseMove" && event.modifiers?.includes("leftButtonDown")) inputCancel.abort();
			return sendInputEvent(event);
		};
		await expect(dragInAppWindow([windowWith([input.view])] as never, args, inputCancel.signal)).rejects.toMatchObject({ code: "APP_DRAG_ABORTED" });
		expect(input.sent.map(({ type, x, y }) => `${type}@${x},${y}`)).toEqual(["mouseMove@10,20", "mouseDown@10,20", "mouseMove@20,30", "mouseUp@20,30"]);
	});
});
