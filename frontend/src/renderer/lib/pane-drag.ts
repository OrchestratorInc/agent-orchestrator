import type { ClientRect } from "./pane-split";

/**
 * Dragging a tab out of a tab strip to split the center pane.
 *
 * A tab press starts its strip's own reorder drag. Once the pointer leaves the
 * strip vertically, or the press is held still for a moment, the tab lifts
 * out (so a held tab can also be moved sideways onto another tab to merge): the strip's reorder stops, a ghost of
 * the tab follows the pointer and the drop target (the session view) shows
 * where the tab would land. Everything during the drag is imperative DOM work
 * batched to one write per frame: the ghost moves by transform, the preview
 * box only moves when the zone under the pointer changes, and React renders
 * nothing until the drop commits.
 */

export type PaneDropResolution = {
	/** Viewport box the tab would occupy. */
	rect: ClientRect;
	/** A short line shown inside the preview, naming what releasing here does. */
	label?: string;
	commit: () => void;
};

export type PaneDropTarget = {
	/** Whether this tab can leave its strip at all. */
	canDrag: (key: string) => boolean;
	resolve: (key: string, x: number, y: number) => PaneDropResolution | null;
};

let dropTarget: PaneDropTarget | null = null;

/** The session view registers itself while it can take dropped tabs. */
export function registerPaneDropTarget(target: PaneDropTarget): () => void {
	dropTarget = target;
	return () => {
		if (dropTarget === target) dropTarget = null;
	};
}

/** Distance past the strip's edge before a tab lifts out of it. */
const DETACH_DISTANCE_PX = 14;
/** A press held this long without moving lifts the tab out in place. */
const HOLD_MS = 320;
/** Movement within this distance still counts as holding still. */
const HOLD_SLOP_PX = 5;
/** How far a pane header moves before it lifts. */
const LIFT_DISTANCE_PX = 4;
/** Matches the preview's CSS fade so the nodes leave after it. */
const FADE_MS = 150;

type ActiveDrag = {
	key: string;
	pointerId: number;
	source: HTMLElement;
	strip: DOMRect;
	detached: boolean;
	grabX: number;
	grabY: number;
	x: number;
	y: number;
	startX: number;
	startY: number;
	/** What the press landed on; a slow click is replayed there. */
	pressTarget: EventTarget | null;
	holdTimer: number | null;
	frame: number | null;
	ghost: HTMLElement | null;
	preview: HTMLElement | null;
	previewShown: boolean;
	resolution: PaneDropResolution | null;
	onDetach?: () => void;
	/** A pane header lifts on the first real movement, not at the strip's edge. */
	immediate: boolean;
	cleanup: () => void;
};

let active: ActiveDrag | null = null;

export function isPaneTabDragging(): boolean {
	return Boolean(active?.detached);
}

/**
 * Call from a tab's pointerdown. `source` is the element that looks like the
 * tab (it is cloned for the ghost). `onDetach` stops the strip's own drag.
 */
export function beginPaneTabDrag(
	event: { pointerId: number; clientX: number; clientY: number; button: number; target?: EventTarget | null },
	key: string,
	source: HTMLElement,
	onDetach?: () => void,
	options?: { immediate?: boolean },
): void {
	if (event.button !== 0 || active) return;
	if (!dropTarget?.canDrag(key)) return;
	const strip = (source.closest("[role=tablist]") ?? source).getBoundingClientRect();
	const drag: ActiveDrag = {
		key,
		pointerId: event.pointerId,
		source,
		strip,
		detached: false,
		grabX: 0,
		grabY: 0,
		x: event.clientX,
		y: event.clientY,
		startX: event.clientX,
		startY: event.clientY,
		pressTarget: event.target ?? null,
		holdTimer: null,
		frame: null,
		ghost: null,
		preview: null,
		previewShown: false,
		resolution: null,
		onDetach,
		immediate: Boolean(options?.immediate),
		cleanup: () => {},
	};
	const onMove = (moveEvent: PointerEvent) => {
		if (moveEvent.pointerId !== drag.pointerId) return;
		drag.x = moveEvent.clientX;
		drag.y = moveEvent.clientY;
		if (!drag.detached) {
			// Moving off the spot first makes this a reorder, not a hold.
			if (drag.holdTimer !== null && Math.hypot(drag.x - drag.startX, drag.y - drag.startY) > HOLD_SLOP_PX) {
				window.clearTimeout(drag.holdTimer);
				drag.holdTimer = null;
			}
			const outside = drag.immediate
				? Math.hypot(drag.x - drag.startX, drag.y - drag.startY) > LIFT_DISTANCE_PX
				: drag.y > drag.strip.bottom + DETACH_DISTANCE_PX || drag.y < drag.strip.top - DETACH_DISTANCE_PX;
			if (!outside) return;
			detach(drag);
		}
		schedule(drag);
	};
	const onUp = (upEvent: PointerEvent) => {
		if (upEvent.pointerId !== drag.pointerId) return;
		drag.x = upEvent.clientX;
		drag.y = upEvent.clientY;
		finish(drag, true);
	};
	const onCancel = (cancelEvent: PointerEvent) => {
		if (cancelEvent.pointerId !== drag.pointerId) return;
		finish(drag, false);
	};
	const onKey = (keyEvent: KeyboardEvent) => {
		if (keyEvent.key !== "Escape" || !drag.detached) return;
		keyEvent.preventDefault();
		keyEvent.stopPropagation();
		finish(drag, false);
	};
	const onBlur = () => finish(drag, false);
	window.addEventListener("pointermove", onMove, true);
	window.addEventListener("pointerup", onUp, true);
	window.addEventListener("pointercancel", onCancel, true);
	window.addEventListener("keydown", onKey, true);
	window.addEventListener("blur", onBlur);
	drag.cleanup = () => {
		window.removeEventListener("pointermove", onMove, true);
		window.removeEventListener("pointerup", onUp, true);
		window.removeEventListener("pointercancel", onCancel, true);
		window.removeEventListener("keydown", onKey, true);
		window.removeEventListener("blur", onBlur);
	};
	// A pane header lifts by moving; a press that stays put is just a click.
	if (!drag.immediate) drag.holdTimer = window.setTimeout(() => {
		drag.holdTimer = null;
		if (active !== drag || drag.detached) return;
		detach(drag);
		schedule(drag);
	}, HOLD_MS);
	active = drag;
}

function detach(drag: ActiveDrag): void {
	drag.detached = true;
	drag.onDetach?.();
	// The ghost is a copy of the tab itself at its exact size, measured from
	// the tab's frame rather than the wrapper the press landed on.
	const frameSelector = "[data-terminal-tab-frame], [data-pane-drag-chip]";
	const frame = (drag.source.matches(frameSelector)
		? drag.source
		: drag.source.querySelector<HTMLElement>(frameSelector)) ?? drag.source;
	const box = frame.getBoundingClientRect();
	drag.grabX = Math.min(Math.max(drag.x - box.left, 0), box.width);
	drag.grabY = Math.min(Math.max(drag.y - box.top, 0), box.height);
	// Keep the pointer stream even over iframes and native-feeling surfaces.
	try {
		drag.source.setPointerCapture(drag.pointerId);
	} catch {
		// The pointer may already be released; pointerup ends the drag anyway.
	}
	const ghost = document.createElement("div");
	ghost.className = "pane-drag-ghost";
	// Tabs in the floating top bar are styled by that bar; carry it over so
	// the ghost looks like the tab it lifted.
	if (drag.source.closest(".center-panel-shell--floating-topbar")) ghost.classList.add("center-panel-shell--floating-topbar");
	ghost.setAttribute("aria-hidden", "true");
	ghost.style.width = `${box.width}px`;
	ghost.style.height = `${box.height}px`;
	const clone = frame.cloneNode(true) as HTMLElement;
	clone.removeAttribute("id");
	clone.querySelectorAll("[id]").forEach((node) => node.removeAttribute("id"));
	clone.removeAttribute("data-testid");
	ghost.appendChild(clone);
	ghost.style.transform = ghostTransform(drag);
	document.body.appendChild(ghost);
	const preview = document.createElement("div");
	preview.className = "pane-drop-preview";
	preview.setAttribute("aria-hidden", "true");
	document.body.appendChild(preview);
	drag.ghost = ghost;
	drag.preview = preview;
	drag.source.setAttribute("data-pane-drag-source", "");
	document.body.classList.add("is-pane-dragging");
	// Lift on the next frame so the ghost fades and scales in from the tab.
	requestAnimationFrame(() => ghost.setAttribute("data-lifted", ""));
}

function ghostTransform(drag: ActiveDrag): string {
	return `translate3d(${Math.round(drag.x - drag.grabX)}px, ${Math.round(drag.y - drag.grabY)}px, 0)`;
}

function schedule(drag: ActiveDrag): void {
	if (drag.frame !== null) return;
	drag.frame = requestAnimationFrame(() => {
		drag.frame = null;
		if (active !== drag || !drag.detached) return;
		paint(drag);
	});
}

function paint(drag: ActiveDrag): void {
	if (drag.ghost) drag.ghost.style.transform = ghostTransform(drag);
	const resolution = dropTarget?.resolve(drag.key, drag.x, drag.y) ?? null;
	drag.resolution = resolution;
	const preview = drag.preview;
	if (!preview) return;
	if (!resolution) {
		if (drag.previewShown) preview.removeAttribute("data-visible");
		drag.previewShown = false;
		return;
	}
	// The label can change while the box stays (a pane header over its own strip).
	if ((preview.dataset.label ?? "") !== (resolution.label ?? "")) {
		if (resolution.label) preview.dataset.label = resolution.label;
		else delete preview.dataset.label;
	}
	const { left, top, width, height } = resolution.rect;
	const next = `${left}|${top}|${width}|${height}`;
	if (preview.dataset.box === next && drag.previewShown) return;
	if (!drag.previewShown) {
		// Appear in place: no slide in from wherever the box last was.
		preview.setAttribute("data-instant", "");
		placePreview(preview, resolution.rect);
		void preview.offsetWidth;
		preview.removeAttribute("data-instant");
		preview.setAttribute("data-visible", "");
		drag.previewShown = true;
	} else {
		placePreview(preview, resolution.rect);
	}
	preview.dataset.box = next;
}

/** The preview floats a little inside the pane it stands for. */
const PREVIEW_INSET_PX = 4;

function placePreview(preview: HTMLElement, rect: ClientRect): void {
	preview.style.left = `${rect.left + PREVIEW_INSET_PX}px`;
	preview.style.top = `${rect.top + PREVIEW_INSET_PX}px`;
	preview.style.width = `${Math.max(0, rect.width - 2 * PREVIEW_INSET_PX)}px`;
	preview.style.height = `${Math.max(0, rect.height - 2 * PREVIEW_INSET_PX)}px`;
}

function finish(drag: ActiveDrag, commit: boolean): void {
	if (active !== drag) return;
	active = null;
	drag.cleanup();
	if (drag.holdTimer !== null) window.clearTimeout(drag.holdTimer);
	if (drag.frame !== null) cancelAnimationFrame(drag.frame);
	if (!drag.detached) return;
	// The release lands on the captured tab, not what was pressed; that click
	// must not act.
	const swallowClick = (clickEvent: MouseEvent) => {
		clickEvent.stopPropagation();
		clickEvent.preventDefault();
	};
	window.addEventListener("click", swallowClick, true);
	// A tab lifted by holding and released where it was pressed was a slow
	// click: put it down and replay the click on what was pressed, so it still
	// selects the tab.
	const stayedPut = Math.hypot(drag.x - drag.startX, drag.y - drag.startY) <= HOLD_SLOP_PX;
	window.setTimeout(() => {
		window.removeEventListener("click", swallowClick, true);
		if (commit && stayedPut && drag.pressTarget instanceof HTMLElement && drag.pressTarget.isConnected) drag.pressTarget.click();
	}, 0);
	// Resolve against the final pointer position, not the last painted frame.
	const resolution = commit && !stayedPut ? (dropTarget?.resolve(drag.key, drag.x, drag.y) ?? null) : null;
	drag.source.removeAttribute("data-pane-drag-source");
	document.body.classList.remove("is-pane-dragging");
	const { ghost, preview } = drag;
	if (ghost) {
		ghost.removeAttribute("data-lifted");
		ghost.setAttribute("data-leaving", "");
	}
	preview?.removeAttribute("data-visible");
	window.setTimeout(() => {
		ghost?.remove();
		preview?.remove();
	}, FADE_MS);
	resolution?.commit();
}
