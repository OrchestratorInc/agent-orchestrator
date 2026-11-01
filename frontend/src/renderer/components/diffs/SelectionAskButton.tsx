import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { selectionActionClass, selectionRangesEqual, selectionToolbarClass, selectionToolbarPosition, selectionViewport } from "../../lib/selection-toolbar";
import { isMacPlatform } from "../../lib/platform";
import { isDialogOrMenuOpen } from "../../lib/dom-selectors";
import { codeSelectionRange, type CodeSelection, type CodeSelectionSource } from "./useCodeSelection";

function boundsFor(selection: CodeSelection) {
	const bounds = { ...selectionViewport(selection.viewport) };
	if (selection.toolbarHeader?.isConnected) bounds.top = Math.max(bounds.top, selection.toolbarHeader.getBoundingClientRect().bottom);
	return bounds;
}

function placement(selection: CodeSelection, button: HTMLElement, bounds = boundsFor(selection)) {
	const rect = selection.textRange?.getBoundingClientRect() ?? new DOMRect(selection.point.x, selection.point.y, 1, 1);
	const connected = !selection.textRange || (selection.host.isConnected && selection.textRange.startContainer.isConnected && selection.textRange.endContainer.isConnected);
	return { bounds, connected, position: selectionToolbarPosition(rect, bounds, button.getBoundingClientRect()) };
}

// Keep highlighted code actionable in its visible pane, including tall ranges.
// Dragging passes through the toolbar; activation preserves the captured rows.
export function SelectionAskButton({ onAsk, onDismiss, source }: {
	onAsk: () => boolean | void; onDismiss?: () => void; source: CodeSelectionSource;
}) {
	const { t } = useTranslation();
	const selection = useSyncExternalStore(source.subscribe, source.get, () => null);
	const buttonRef = useRef<HTMLDivElement>(null);
	const [rejected, setRejected] = useState(false);

	useEffect(() => setRejected(false), [selection]);
	const submitted = useRef<CodeSelection | null>(null);
	const [accepted, setAccepted] = useState<CodeSelection | null>(null);
	const dismissedEscapePress = useRef(false);
	const activate = useCallback(() => {
		if (!selection || selection.dragging || submitted.current === selection) return;
		submitted.current = selection;
		try {
			if (onAsk() === false) { submitted.current = null; setRejected(true); }
			else setAccepted(selection);
		} catch (error) {
			submitted.current = null;
			throw error;
		}
	}, [selection, onAsk]);
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.defaultPrevented || isDialogOrMenuOpen()) return;
			if (event.key === "Escape" && event.repeat && dismissedEscapePress.current) {
				event.preventDefault(); event.stopPropagation(); return;
			}
			const escape = event.key === "Escape" && !event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && Boolean(onDismiss);
			const add = event.key.toLowerCase() === "l" && !event.shiftKey && !event.altKey &&
				(isMacPlatform() ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey);
			const button = buttonRef.current;
			if ((!escape && !add) || !selection || selection.dragging || !button || submitted.current === selection) return;
			const geometry = placement(selection, button);
			if (!geometry.connected || !geometry.position.visible || getComputedStyle(button).visibility === "hidden") return;
			const origin = event.composedPath()[0];
			let target = origin instanceof Element ? origin : document.activeElement;
			if (target === document.body || target === document.documentElement) target = document.activeElement;
			if (target?.closest('input, textarea, select, [contenteditable], [role="textbox"], .xterm, webview, [data-browser-view]')) return;
			const toolbarFocused = Boolean(target && button.contains(target));
			const surface = selection.host?.closest("[data-selection-toolbar-scope], .ao-pierre-surface") ?? selection.host;
			const onSurface = Boolean(surface && target && (surface.contains(target) || event.composedPath().includes(surface)));
			const liveRange = selection.host && codeSelectionRange(selection.host);
			const matches = selectionRangesEqual(liveRange ?? undefined, selection.textRange);
			const documentTarget = !target || target === document.body || target === document.documentElement;
			if (!toolbarFocused && (!matches || (!onSurface && !documentTarget))) return;
			if (toolbarFocused && !matches && liveRange && !liveRange.collapsed) return;
			if (escape && !event.repeat) {
				dismissedEscapePress.current = true;
				event.preventDefault(); event.stopPropagation(); onDismiss!();
			} else if (add) {
				event.preventDefault();
				event.stopPropagation();
				if (!event.repeat) activate();
			}
		};
		const release = (event: KeyboardEvent) => { if (event.key === "Escape") dismissedEscapePress.current = false; };
		const blur = () => { dismissedEscapePress.current = false; };
		document.addEventListener("keydown", onKeyDown, true);
		document.addEventListener("keyup", release, true);
		window.addEventListener("blur", blur);
		return () => {
			document.removeEventListener("keydown", onKeyDown, true);
			document.removeEventListener("keyup", release, true);
			window.removeEventListener("blur", blur);
		};
	}, [selection, activate, onDismiss]);
	// Geometry is independent of the code capture store and the Pierre surface.
	useLayoutEffect(() => {
		const button = buttonRef.current;
		if (!button || !selection) return;
		let frame: number | undefined;
		let disposed = false;
		const place = () => {
			if (disposed) return;
			frame = undefined;
			const bounds = boundsFor(selection);
			const maxWidth = `${Math.max(0, bounds.right - bounds.left - 16)}px`;
			if (button.style.maxWidth !== maxWidth) button.style.maxWidth = maxWidth;
			const { position, connected } = placement(selection, button, bounds);
			button.style.left = `${position.left}px`;
			button.style.top = `${position.top}px`;
			button.style.visibility = position.visible && connected ? "visible" : "hidden";
		};
		const schedule = () => { if (!disposed) frame ??= requestAnimationFrame(place); };
		const resize = new ResizeObserver(schedule);
		resize.observe(button);
		if (selection.viewport) resize.observe(selection.viewport);
		if (selection.host) resize.observe(selection.host);
		if (selection.toolbarHeader) resize.observe(selection.toolbarHeader);
		document.addEventListener("scroll", schedule, true);
		window.addEventListener("resize", schedule);
		window.visualViewport?.addEventListener("resize", schedule);
		window.visualViewport?.addEventListener("scroll", schedule);
		place();
		return () => {
			disposed = true;
			if (frame !== undefined) cancelAnimationFrame(frame);
			resize.disconnect();
			document.removeEventListener("scroll", schedule, true);
			window.removeEventListener("resize", schedule);
			window.visualViewport?.removeEventListener("resize", schedule);
			window.visualViewport?.removeEventListener("scroll", schedule);
		};
	}, [selection]);
	if (!selection) return null;
	const label = t("files.askInChat");
	return createPortal(
		<div
			className={selectionToolbarClass}
			data-selection-ask=""
			onMouseDown={(event) => event.preventDefault()}
			ref={buttonRef}
			// Mid-drag it sits under the pointer: let the drag pass through it.
			style={{
				left: selection.point.x + 6,
				opacity: selection.dragging ? 0.85 : 1,
				pointerEvents: selection.dragging ? "none" : undefined,
				position: "fixed",
				top: selection.point.y - 14,
			}}
		>
			<button
				className={selectionActionClass}
				onClick={activate}
				disabled={selection.dragging || accepted === selection}
				title={`${label} (${isMacPlatform() ? "⌘L" : "Ctrl+L"})`}
				type="button"
			>
				{label}
			</button>
			{rejected ? <span role="alert" className="max-w-56 px-2 text-xs text-muted-foreground">{t("files.addToChatRejected")}</span> : null}
		</div>,
		document.body,
	);
}
