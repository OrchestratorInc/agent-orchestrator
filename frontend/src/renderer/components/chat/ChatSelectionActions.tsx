import { isMacPlatform } from "../../lib/platform";
import { isDialogOrMenuOpen } from "../../lib/dom-selectors";
import { useLatestCallback } from "../../hooks/useStable";
import { useEffect, useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { useChatSelectionPosition } from "../../hooks/useChatSelectionPosition";
import { apiErrorMessage } from "../../lib/api-client";
import {
	selectionActionClass,
	selectionRangesEqual,
	selectionToolbarClass,
} from "../../lib/selection-toolbar";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";

/** Geometry state stays here: scrolling must not render the conversation. */
export function ChatSelectionActions({
	selection,
	viewportRef,
	onInvalid,
	onAdd,
	onSide,
	checkingSide,
}: {
	selection: { excerpt: ChatDraftExcerptReference; range: Range };
	viewportRef: RefObject<HTMLDivElement | null>;
	onInvalid: () => void;
	onAdd: () => void | Promise<void>;
	onSide?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	checkingSide?: boolean;
}) {
	const { t } = useTranslation();
	const buttonRef = useRef<HTMLDivElement>(null);
	const position = useChatSelectionPosition(
		selection.range,
		viewportRef,
		buttonRef,
		onInvalid,
	);
	const locked = useRef(false);
	const alive = useRef(true);
	useEffect(() => {
		alive.current = true;
		return () => {
			alive.current = false;
		};
	}, []);
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<string>();
	const activate = useLatestCallback(async (side: boolean) => {
		if (locked.current || (side && (!onSide || checkingSide))) return;
		locked.current = true;
		setPending(true);
		setError(undefined);
		try {
			if (side) {
				await onSide!(selection.excerpt);
				if (!alive.current) return;
				onInvalid();
				const current = window.getSelection();
				const range = current?.rangeCount ? current.getRangeAt(0) : undefined;
				if (selectionRangesEqual(range, selection.range))
					current?.removeAllRanges();
			} else await onAdd();
		} catch (failure) {
			if (alive.current) setError(apiErrorMessage(failure));
		} finally {
			locked.current = false;
			if (alive.current) setPending(false);
		}
	});
	useEffect(() => {
		if (!position?.visible) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (
				event.defaultPrevented ||
				event.key.toLowerCase() !== "l" ||
				event.shiftKey ||
				event.altKey ||
				(isMacPlatform()
					? !event.metaKey || event.ctrlKey
					: !event.ctrlKey || event.metaKey) ||
				isDialogOrMenuOpen()
			)
				return;
			const toolbar = buttonRef.current;
			const origin = event.composedPath()[0];
			const target =
				origin instanceof Element ? origin : document.activeElement;
			if (
				!toolbar ||
				target?.closest(
					'input, textarea, select, [contenteditable], [role="textbox"], .xterm, webview, [data-browser-view]',
				)
			)
				return;
			if (
				target &&
				target !== document.body &&
				target !== document.documentElement &&
				!viewportRef.current?.contains(target) &&
				!toolbar.contains(target)
			)
				return;
			const native = window.getSelection();
			const saved = selection.range;
			if (
				!native?.rangeCount ||
				native.isCollapsed ||
				!saved.startContainer.isConnected ||
				!saved.endContainer.isConnected ||
				!viewportRef.current?.contains(saved.startContainer) ||
				!selectionRangesEqual(native.getRangeAt(0), saved)
			)
				return;
			event.preventDefault();
			event.stopPropagation();
			if (!event.repeat) void activate(false);
		};
		document.addEventListener("keydown", onKeyDown, true);
		return () => document.removeEventListener("keydown", onKeyDown, true);
	}, [position?.visible, selection, viewportRef, activate]);

	return (
		<div
			ref={buttonRef}
			data-chat-selection-actions=""
			className={`${selectionToolbarClass} absolute ${position?.visible ? "" : "invisible"}`}
			style={{ left: position?.left ?? 0, top: position?.top ?? 0 }}
			onMouseDown={(event) => event.preventDefault()}
		>
			<button
				type="button"
				title={`${t("sideChat.addToChat")} (${isMacPlatform() ? "⌘L" : "Ctrl+L"})`}
				className={selectionActionClass}
				disabled={pending}
				onClick={() => void activate(false)}
			>
				{t("sideChat.addToChat")}
			</button>
			{onSide || checkingSide ? (
				<button
					type="button"
					className={selectionActionClass}
					disabled={pending || checkingSide}
					onClick={() => void activate(true)}
				>
					{t("sideChat.addToSideChat")}
				</button>
			) : null}
			{error ? (
				<span
					role="alert"
					className="max-w-full px-2 text-xs text-destructive break-words"
				>
					{error}
				</span>
			) : null}
		</div>
	);
}
