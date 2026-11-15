import { createContext, useContext, useEffect, useSyncExternalStore } from "react";
import { artifactZoomAction, type ArtifactZoomAction } from "../../shared/shortcuts";

export type ZoomAnchor = { clientX: number; clientY: number };

export const MIN_ARTIFACT_ZOOM = 0.25;
export const MAX_ARTIFACT_ZOOM = 5;
export const ZOOM_CONTROLS_REVEAL_PX = 96;

const ZOOM_STEPS = [0.25, 0.33, 0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3, 4, 5];

function clampZoom(zoom: number): number {
	return Math.min(MAX_ARTIFACT_ZOOM, Math.max(MIN_ARTIFACT_ZOOM, zoom));
}

export function steppedZoom(zoom: number, direction: 1 | -1): number {
	const next = direction > 0
		? ZOOM_STEPS.find((step) => step > zoom + 0.001)
		: [...ZOOM_STEPS].reverse().find((step) => step < zoom - 0.001);
	return next ?? zoom;
}

export function wheelZoomFactor(event: Pick<WheelEvent, "deltaMode" | "deltaY">): number {
	const delta = event.deltaMode === 1 ? event.deltaY * 33 : event.deltaY;
	return Math.exp(-Math.max(-25, Math.min(25, delta)) / 100);
}

type Listener = () => void;

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

	onReveal = (listener: Listener) => {
		this.revealListeners.add(listener);
		return () => { this.revealListeners.delete(listener); };
	};

	reveal() {
		for (const listener of this.revealListeners) listener();
	}

	getZoom = () => this.zoom;
	hasTarget = () => this.targets > 0;

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

export function useArtifactZoomController(): ArtifactZoomController | null {
	return useContext(ArtifactZoomContext);
}

export function useArtifactZoomTarget(enabled = true): { controller: ArtifactZoomController | null; zoom: number } {
	const controller = useArtifactZoomController();
	const active = enabled ? controller : null;
	useEffect(() => active?.register(), [active]);
	const zoom = useSyncExternalStore(active?.subscribe ?? noopSubscribe, active?.getZoom ?? one);
	return { controller: active, zoom };
}

export function useArtifactZoomRegistration(enabled = true): ArtifactZoomController | null {
	const controller = useArtifactZoomController();
	const active = enabled ? controller : null;
	useEffect(() => active?.register(), [active]);
	return active;
}

export function useArtifactZoomState(controller: ArtifactZoomController | null) {
	const zoom = useSyncExternalStore(controller?.subscribe ?? noopSubscribe, controller?.getZoom ?? one);
	const available = useSyncExternalStore(controller?.subscribe ?? noopSubscribe, controller?.hasTarget ?? no);
	return { available, zoom };
}

export function zoomShortcut(event: Pick<KeyboardEvent, "altKey" | "code" | "ctrlKey" | "key" | "metaKey">, mac: boolean): ArtifactZoomAction | undefined {
	return artifactZoomAction({ alt: event.altKey, code: event.code, ctrl: event.ctrlKey, key: event.key, meta: event.metaKey }, mac);
}

export function applyZoomShortcut(controller: ArtifactZoomController, action: ArtifactZoomAction) {
	if (action === "reset") controller.reset();
	else controller.step(action === "in" ? 1 : -1);
}

const noopSubscribe = () => () => {};
const one = () => 1;
const no = () => false;
