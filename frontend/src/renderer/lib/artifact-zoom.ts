import { createContext, useContext, useEffect, useSyncExternalStore } from "react";
import { artifactZoomAction, type ArtifactZoomAction } from "../../shared/shortcuts";

/** A point in window client coordinates that a zoom step keeps in place. */
export type ZoomAnchor = { clientX: number; clientY: number };

export const MIN_ARTIFACT_ZOOM = 0.25;
export const MAX_ARTIFACT_ZOOM = 5;
/** A pointer this close to the preview's bottom edge brings the zoom controls back. */
export const ZOOM_CONTROLS_REVEAL_PX = 96;

// The steps the zoom buttons and Cmd+=/Cmd+- move between, as a browser's do.
const ZOOM_STEPS = [0.25, 0.33, 0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3, 4, 5];

function clampZoom(zoom: number): number {
	return Math.min(MAX_ARTIFACT_ZOOM, Math.max(MIN_ARTIFACT_ZOOM, zoom));
}

/** The next step above (1) or below (-1) zoom. */
export function steppedZoom(zoom: number, direction: 1 | -1): number {
	const next = direction > 0
		? ZOOM_STEPS.find((step) => step > zoom + 0.001)
		: [...ZOOM_STEPS].reverse().find((step) => step < zoom - 0.001);
	return next ?? zoom;
}

/** The zoom factor one wheel event asks for: a pinch arrives as a wheel event with ctrlKey. */
export function wheelZoomFactor(event: Pick<WheelEvent, "deltaMode" | "deltaY">): number {
	// Chromium sends a pinch as deltaY = -100 * ln(scale), so this tracks the
	// fingers. A Ctrl+mouse wheel notch (100px, or 3 lines) is capped to ~1.28x.
	const delta = event.deltaMode === 1 ? event.deltaY * 33 : event.deltaY;
	return Math.exp(-Math.max(-25, Math.min(25, delta)) / 100);
}

type Listener = () => void;

/**
 * The open artifact's zoom: 1 is the viewer's fit. One renderer at a time
 * (image, PDF, page or document) registers as the target and applies it; the
 * header's controls appear only while a target is registered. Kept outside
 * React state so a pinch does not re-render the whole artifact view.
 */
export class ArtifactZoomController {
	private zoom = 1;
	private targets = 0;
	private anchor: ZoomAnchor | undefined;
	private listeners = new Set<Listener>();
	private revealListeners = new Set<Listener>();

	subscribe = (listener: Listener) => {
		this.listeners.add(listener);
		return () => { this.listeners.delete(listener); };
	};

	/** Listens for the pointer nearing the zoom controls somewhere the view cannot see it, as inside a framed page. */
	onReveal = (listener: Listener) => {
		this.revealListeners.add(listener);
		return () => { this.revealListeners.delete(listener); };
	};

	reveal() {
		for (const listener of this.revealListeners) listener();
	}

	getZoom = () => this.zoom;
	hasTarget = () => this.targets > 0;

	/** The point the last change was anchored at, read once by the target applying it. */
	takeAnchor(): ZoomAnchor | undefined {
		const anchor = this.anchor;
		this.anchor = undefined;
		return anchor;
	}

	setZoom(zoom: number, anchor?: ZoomAnchor) {
		const next = clampZoom(zoom);
		if (Math.abs(next - this.zoom) < 0.0005) return;
		this.zoom = next;
		this.anchor = anchor;
		this.emit();
	}

	zoomBy(factor: number, anchor?: ZoomAnchor) {
		this.setZoom(this.zoom * factor, anchor);
	}

	step(direction: 1 | -1) {
		this.setZoom(steppedZoom(this.zoom, direction));
	}

	reset() {
		this.setZoom(1);
	}

	register() {
		this.targets += 1;
		this.emit();
		return () => {
			this.targets -= 1;
			// The next target starts at fit.
			this.zoom = 1;
			this.anchor = undefined;
			this.emit();
		};
	}

	private emit() {
		for (const listener of this.listeners) listener();
	}
}

export const ArtifactZoomContext = createContext<ArtifactZoomController | null>(null);

/** The zoom controller this viewer applies, or null outside a zoomable artifact view. */
export function useArtifactZoomController(): ArtifactZoomController | null {
	return useContext(ArtifactZoomContext);
}

/** Registers the caller as the zoom target while enabled; returns the zoom it applies (1 when not zoomable). */
export function useArtifactZoomTarget(enabled = true): { controller: ArtifactZoomController | null; zoom: number } {
	const controller = useArtifactZoomController();
	const active = enabled ? controller : null;
	useEffect(() => active?.register(), [active]);
	const zoom = useSyncExternalStore(active?.subscribe ?? noopSubscribe, active?.getZoom ?? one);
	return { controller: active, zoom };
}

/**
 * Registers the caller as the zoom target while enabled, without re-rendering
 * it on each zoom: the caller applies the zoom itself through
 * controller.subscribe, as a pinch changes it every frame.
 */
export function useArtifactZoomRegistration(enabled = true): ArtifactZoomController | null {
	const controller = useArtifactZoomController();
	const active = enabled ? controller : null;
	useEffect(() => active?.register(), [active]);
	return active;
}

/** Reads whether a target is registered and its zoom, for the controls. */
export function useArtifactZoomState(controller: ArtifactZoomController | null) {
	const zoom = useSyncExternalStore(controller?.subscribe ?? noopSubscribe, controller?.getZoom ?? one);
	const available = useSyncExternalStore(controller?.subscribe ?? noopSubscribe, controller?.hasTarget ?? no);
	return { available, zoom };
}

/** The zoom shortcut a keydown is, if any: Cmd+= / Cmd+- / Cmd+0 (Ctrl elsewhere). */
export function zoomShortcut(event: Pick<KeyboardEvent, "altKey" | "code" | "ctrlKey" | "key" | "metaKey">, mac: boolean): ArtifactZoomAction | undefined {
	return artifactZoomAction({ alt: event.altKey, code: event.code, ctrl: event.ctrlKey, key: event.key, meta: event.metaKey }, mac);
}

/** Applies a zoom shortcut to the controller. */
export function applyZoomShortcut(controller: ArtifactZoomController, action: ArtifactZoomAction) {
	if (action === "reset") controller.reset();
	else controller.step(action === "in" ? 1 : -1);
}

const noopSubscribe = () => () => {};
const one = () => 1;
const no = () => false;
