import type { BaseWindow, View, WebContents } from "electron";

type Point = [number, number];

type DragWindow = Pick<BaseWindow, "getMediaSourceId" | "getBounds" | "getContentBounds" | "isDestroyed" | "contentView">;

type PageView = View & { webContents: WebContents };

export type AppDragResult = {
	ok: true;
	route: "cdp" | "send_input_event";
	dragIntercepted: boolean;
	html5Drop: boolean;
};

const CDP_MODIFIERS: Record<string, number> = { alt: 1, ctrl: 2, meta: 4, shift: 8 };
const MODIFIER_NAMES: Record<string, string> = {
	cmd: "meta", command: "meta", meta: "meta",
	ctrl: "ctrl", control: "ctrl",
	option: "alt", alt: "alt",
	shift: "shift",
};
const CDP_BUTTONS: Record<string, number> = { left: 1, right: 2, middle: 4 };

function fail(code: string, message: string): never {
	throw Object.assign(new Error(message), { code });
}

function finite(value: unknown, name: string): number {
	const number = Number(value);
	if (!Number.isFinite(number)) fail("APP_DRAG_INVALID", `${name} must be a number`);
	return number;
}

function findWindow(windows: readonly DragWindow[], windowId: number): DragWindow | undefined {
	return windows.find((window) => {
		try {
			return !window.isDestroyed() && String(window.getMediaSourceId()).split(":")[1] === String(windowId);
		} catch {
			return false;
		}
	});
}

function pageViewAt(window: DragWindow, [x, y]: Point): PageView | undefined {
	const children = [...window.contentView.children].reverse();
	return children.find((view): view is PageView => {
		const page = view as Partial<PageView>;
		if (!page.webContents || page.webContents.isDestroyed()) return false;
		if (typeof view.getVisible === "function" && !view.getVisible()) return false;
		const b = view.getBounds();
		return x >= b.x && y >= b.y && x < b.x + b.width && y < b.y + b.height;
	});
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

let gesture: Promise<unknown> = Promise.resolve();

export function dragInAppWindow(
	windows: readonly DragWindow[],
	args: Record<string, unknown>,
	signal?: AbortSignal,
): Promise<AppDragResult> {
	const run = gesture.then(() => runDrag(windows, args, signal));
	gesture = run.catch(() => {});
	return run;
}

async function runDrag(
	windows: readonly DragWindow[],
	args: Record<string, unknown>,
	signal?: AbortSignal,
): Promise<AppDragResult> {
	if (signal?.aborted) fail("APP_DRAG_ABORTED", "The drag was cancelled before it started.");
	const window = findWindow(windows, finite(args.windowId, "windowId"));
	if (!window) fail("APP_WINDOW_NOT_FOUND", "No Orchestrator window has this window_id; read the Orchestrator windows with list_windows again.");
	const bounds = window.getBounds();
	const content = window.getContentBounds();
	const local = (x: unknown, y: unknown, name: string): Point => [
		finite(x, `${name}X`) - (content.x - bounds.x),
		finite(y, `${name}Y`) - (content.y - bounds.y),
	];
	const from = local(args.fromX, args.fromY, "from");
	const to = local(args.toX, args.toY, "to");
	for (const [x, y] of [from, to]) {
		if (x < 0 || y < 0 || x >= content.width || y >= content.height) {
			fail("APP_DRAG_OUTSIDE", "The drag starts or ends outside the window's content.");
		}
	}
	const view = pageViewAt(window, from);
	if (!view) fail("APP_DRAG_NO_PAGE", "No page is showing where the drag starts.");
	const origin = view.getBounds();
	const inView = ([x, y]: Point): Point => [x - origin.x, y - origin.y];
	const start = inView(from);
	const end = inView(to);

	const steps = Math.min(200, Math.max(1, Math.round(Number(args.steps ?? 20)) || 20));
	const duration = Math.min(10_000, Math.max(0, Number(args.durationMs ?? 500) || 0));
	const stepDelay = Math.floor(duration / steps);
	const path: Point[] = [];
	for (let i = 1; i <= steps; i++) {
		path.push([start[0] + ((end[0] - start[0]) * i) / steps, start[1] + ((end[1] - start[1]) * i) / steps]);
	}
	const button = typeof args.button === "string" && args.button in CDP_BUTTONS ? args.button : "left";
	const modifiers = (Array.isArray(args.modifiers) ? args.modifiers : [])
		.map((name) => MODIFIER_NAMES[String(name).toLowerCase()])
		.filter((name): name is string => Boolean(name));
	const wc = view.webContents;
	const dbg = wc.debugger;

	let attached = false;
	if (!dbg.isAttached()) {
		try {
			dbg.attach("1.3");
			attached = true;
		} catch {}
	}
	if (attached) {
		let drag: unknown = null;
		const onMessage = (_event: unknown, method: string, params: { data?: unknown }) => {
			if (method === "Input.dragIntercepted") drag = params.data;
		};
		dbg.on("message", onMessage);
		const zoom = wc.getZoomFactor() || 1;
		const css = ([x, y]: Point) => ({ x: x / zoom, y: y / zoom });
		const mask = modifiers.reduce((sum, name) => sum + (CDP_MODIFIERS[name] ?? 0), 0);
		const mouse = (type: string, p: Point, held: boolean, extra: Record<string, unknown> = {}) =>
			dbg.sendCommand("Input.dispatchMouseEvent", {
				type, ...css(p), modifiers: mask, button, buttons: held ? CDP_BUTTONS[button] : 0,
				clickCount: 1, pointerType: "mouse", ...extra,
			});
		const dragEvent = (type: string, p: Point) =>
			dbg.sendCommand("Input.dispatchDragEvent", { type, ...css(p), data: drag, modifiers: mask });
		let dropped = false;
		try {
			await dbg.sendCommand("Input.setInterceptDrags", { enabled: true });
			await mouse("mouseMoved", start, false, { button: "none", clickCount: 0 });
			await mouse("mousePressed", start, true);
			await sleep(30);
			let entered = false;
			for (const p of path) {
				if (signal?.aborted) break;
				if (drag) {
					if (!entered) await dragEvent("dragEnter", p);
					entered = true;
					await dragEvent("dragOver", p);
				} else {
					await mouse("mouseMoved", p, true, { clickCount: 0 });
				}
				await sleep(stepDelay);
			}
			await sleep(30);
			if (drag && !signal?.aborted) {
				if (!entered) await dragEvent("dragEnter", end);
				await dragEvent("dragOver", end);
				await dragEvent("drop", end);
				dropped = true;
			}
		} finally {
			await mouse("mouseReleased", end, false).catch(() => {});
			dbg.removeListener("message", onMessage);
			await dbg.sendCommand("Input.setInterceptDrags", { enabled: false }).catch(() => {});
			try {
				dbg.detach();
			} catch {}
		}
		return { ok: true, route: "cdp", dragIntercepted: drag !== null, html5Drop: dropped };
	}

	type Modifiers = NonNullable<Electron.InputEvent["modifiers"]>;
	const keys = modifiers.map((name) => (name === "ctrl" ? "control" : name)) as Modifiers;
	const held = `${button}ButtonDown` as Modifiers[number];
	const send = (type: "mouseMove" | "mouseDown" | "mouseUp", [x, y]: Point, down: boolean, clickCount?: number) =>
		wc.sendInputEvent({
			type, x: Math.round(x), y: Math.round(y), button: button as "left" | "right" | "middle",
			modifiers: down ? [...keys, held] : keys,
			...(clickCount === undefined ? {} : { clickCount }),
		});
	send("mouseMove", start, false);
	send("mouseDown", start, true, 1);
	try {
		await sleep(30);
		for (const p of path) {
			if (signal?.aborted) break;
			send("mouseMove", p, true);
			await sleep(stepDelay);
		}
		await sleep(30);
	} finally {
		send("mouseUp", end, false, 1);
	}
	return { ok: true, route: "send_input_event", dragIntercepted: false, html5Drop: false };
}
