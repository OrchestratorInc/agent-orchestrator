import { ipcRenderer } from "electron";
import {
	isRegionLargeEnough,
	normalizeRegionRect,
	type BrowserRegionPoint,
	type BrowserRegionSelectPageMode,
	type BrowserRegionSelectPagePayload,
} from "./shared/browser-region-select";

// The region snip overlay. Main turns it on for the active tab; the person
// drags a rectangle, and the overlay removes itself before reporting the
// rectangle so the capture shows the page, not the overlay.

const HOST_ATTRIBUTE = "data-ao-region-select";

let host: HTMLDivElement | null = null;
let shadow: ShadowRoot | null = null;
let start: BrowserRegionPoint | null = null;
let installed = false;

export function installRegionSelect(): void {
	if (installed) return;
	installed = true;
	ipcRenderer.on("browser:regionSelect:setMode", (_event, input: BrowserRegionSelectPageMode) => {
		if (input?.enabled) enable();
		else teardown();
	});
	window.addEventListener("beforeunload", teardown);
}

function viewport() {
	return { width: window.innerWidth, height: window.innerHeight };
}

function enable(): void {
	if (host) return;
	start = null;
	host = document.createElement("div");
	host.setAttribute(HOST_ATTRIBUTE, "");
	host.style.cssText = "all:initial;position:fixed;inset:0;z-index:2147483647;cursor:crosshair;";
	shadow = host.attachShadow({ mode: "open" });
	shadow.innerHTML = `<style>${styles()}</style><div class="shade"></div><div class="selection" hidden></div><div class="size" hidden></div><div class="hint">Drag to select an area · Esc to cancel</div>`;
	host.addEventListener("pointerdown", onPointerDown);
	host.addEventListener("pointermove", onPointerMove);
	host.addEventListener("pointerup", onPointerUp);
	host.addEventListener("pointercancel", onPointerCancel);
	host.addEventListener("contextmenu", preventDefault);
	window.addEventListener("keydown", onKeyDown, true);
	(document.documentElement ?? document.body).appendChild(host);
}

/** Removes the overlay without telling main; main or the page already knows. */
function teardown(): void {
	window.removeEventListener("keydown", onKeyDown, true);
	host?.remove();
	host = null;
	shadow = null;
	start = null;
}

function cancel(): void {
	if (!host) return;
	teardown();
	ipcRenderer.send("browser:regionSelect:cancel");
}

function preventDefault(event: Event): void {
	event.preventDefault();
}

function onKeyDown(event: KeyboardEvent): void {
	if (event.key !== "Escape") return;
	event.preventDefault();
	event.stopPropagation();
	cancel();
}

function onPointerDown(event: PointerEvent): void {
	if (event.button !== 0) return;
	event.preventDefault();
	start = { x: event.clientX, y: event.clientY };
	host?.setPointerCapture?.(event.pointerId);
	render({ x: event.clientX, y: event.clientY });
}

function onPointerMove(event: PointerEvent): void {
	if (!start) return;
	event.preventDefault();
	render({ x: event.clientX, y: event.clientY });
}

function onPointerUp(event: PointerEvent): void {
	if (!start) return;
	event.preventDefault();
	const rect = normalizeRegionRect(start, { x: event.clientX, y: event.clientY }, viewport());
	if (!isRegionLargeEnough(rect)) {
		cancel();
		return;
	}
	teardown();
	// Two frames: the first commits the overlay's removal, the second is
	// painted without it, so the capture main takes next is the bare page.
	requestAnimationFrame(() =>
		requestAnimationFrame(() => {
			const payload: BrowserRegionSelectPagePayload = { rect };
			ipcRenderer.send("browser:regionSelect:select", payload);
		}),
	);
}

function onPointerCancel(): void {
	start = null;
	render(undefined);
}

function render(current: BrowserRegionPoint | undefined): void {
	const root = shadow;
	if (!root) return;
	const shade = root.querySelector<HTMLElement>(".shade");
	const selection = root.querySelector<HTMLElement>(".selection");
	const size = root.querySelector<HTMLElement>(".size");
	const hint = root.querySelector<HTMLElement>(".hint");
	if (!shade || !selection || !size || !hint) return;
	if (!start || !current) {
		shade.hidden = false;
		selection.hidden = true;
		size.hidden = true;
		hint.hidden = false;
		return;
	}
	const rect = normalizeRegionRect(start, current, viewport());
	// The selection's own shadow dims everything outside it, so the shade
	// steps aside while dragging.
	shade.hidden = true;
	hint.hidden = true;
	selection.hidden = false;
	selection.style.transform = `translate(${rect.x}px, ${rect.y}px)`;
	selection.style.width = `${rect.width}px`;
	selection.style.height = `${rect.height}px`;
	size.hidden = rect.width < 1 && rect.height < 1;
	size.textContent = `${Math.round(rect.width)} × ${Math.round(rect.height)}`;
	const below = rect.y + rect.height + 28 <= window.innerHeight;
	size.style.transform = `translate(${rect.x}px, ${below ? rect.y + rect.height + 6 : Math.max(0, rect.y - 26)}px)`;
}

function styles(): string {
	return `
		:host{all:initial}
		[hidden]{display:none !important}
		.shade{position:fixed;inset:0;background:rgba(0,0,0,.28)}
		.selection{position:fixed;left:0;top:0;box-sizing:border-box;border:1px solid #fff;outline:1px solid rgba(0,0,0,.45);box-shadow:0 0 0 100vmax rgba(0,0,0,.28);pointer-events:none}
		.size,.hint{position:fixed;left:0;top:0;padding:3px 8px;border-radius:6px;background:rgba(17,17,17,.86);color:#fff;font:500 12px/16px ui-sans-serif,system-ui,-apple-system,sans-serif;font-variant-numeric:tabular-nums;white-space:nowrap;pointer-events:none}
		.hint{left:50%;top:12px;transform:translateX(-50%)}
	`;
}
