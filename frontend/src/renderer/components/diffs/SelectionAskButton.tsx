import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { selectionActionClass, selectionToolbarClass, selectionToolbarPosition, selectionViewport } from "../../lib/selection-toolbar";
import { isMacPlatform } from "../../lib/platform";
import type { CodeSelection, CodeSelectionSource } from "./useCodeSelection";

// Keep highlighted code actionable in its visible pane, including tall ranges.
// Dragging passes through the toolbar; activation preserves the captured rows.
export function SelectionAskButton({ onAsk, source }: { onAsk: () => boolean | void; source: CodeSelectionSource }) {
	const { t } = useTranslation();
	const selection = useSyncExternalStore(source.subscribe, source.get, () => null);
	const buttonRef = useRef<HTMLDivElement>(null);
	const [rejected, setRejected] = useState(false);

	useEffect(() => setRejected(false), [selection]);
	const submitted = useRef<CodeSelection | null>(null);
	const [accepted, setAccepted] = useState<CodeSelection | null>(null);
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
		if (!selection) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === "l") {
				event.preventDefault();
				event.stopPropagation();
				activate();
			}
		};
		document.addEventListener("keydown", onKeyDown, true);
		return () => document.removeEventListener("keydown", onKeyDown, true);
	}, [selection, activate]);
	// Geometry is independent of the code capture store and the Pierre surface.
	useLayoutEffect(() => {
		const button = buttonRef.current;
		if (!button || !selection) return;
		let frame: number | undefined;
		let disposed = false;
		const place = () => {
			if (disposed) return;
			frame = undefined;
			const bounds = { ...selectionViewport(selection.viewport) };
			if (selection.toolbarHeader?.isConnected) bounds.top = Math.max(bounds.top, selection.toolbarHeader.getBoundingClientRect().bottom);
			const maxWidth = `${Math.max(0, bounds.right - bounds.left - 16)}px`;
			if (button.style.maxWidth !== maxWidth) button.style.maxWidth = maxWidth;
			const rect = selection.textRange?.getBoundingClientRect() ?? new DOMRect(selection.point.x, selection.point.y, 1, 1);
			const position = selectionToolbarPosition(rect, bounds, button.getBoundingClientRect());
			const connected = !selection.textRange || (selection.host.isConnected && selection.textRange.startContainer.isConnected);
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
