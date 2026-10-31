import { useEffect, useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { useChatSelectionPosition } from "../../hooks/useChatSelectionPosition";
import { apiErrorMessage } from "../../lib/api-client";
import { selectionActionClass, selectionRangesEqual, selectionToolbarClass } from "../../lib/selection-toolbar";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";

/** Geometry state stays here: scrolling must not render the conversation. */
export function ChatSelectionActions({ selection, viewportRef, onInvalid, onAdd, onSide, checkingSide }: {
	selection: { excerpt: ChatDraftExcerptReference; range: Range };
	viewportRef: RefObject<HTMLDivElement | null>;
	onInvalid: () => void;
	onAdd: () => void | Promise<void>;
	onSide?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	checkingSide?: boolean;
}) {
	const { t } = useTranslation();
	const buttonRef = useRef<HTMLDivElement>(null);
	const position = useChatSelectionPosition(selection.range, viewportRef, buttonRef, onInvalid);
	const locked = useRef(false);
	const alive = useRef(true);
	useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<string>();
	const activate = async (side: boolean) => {
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
				if (selectionRangesEqual(range, selection.range)) current?.removeAllRanges();
			} else await onAdd();
		} catch (failure) {
			if (alive.current) setError(apiErrorMessage(failure));
		} finally {
			locked.current = false;
			if (alive.current) setPending(false);
		}
	};
	return <div ref={buttonRef} data-chat-selection-actions="" className={selectionToolbarClass}
		style={{ position: "absolute", left: position?.left ?? 0, top: position?.top ?? 0, visibility: position?.visible ? "visible" : "hidden" }}
		onMouseDown={(event) => event.preventDefault()}>
		<button type="button" className={selectionActionClass} disabled={pending} onClick={() => void activate(false)}>{t("sideChat.addToChat")}</button>
		{onSide || checkingSide ? <button type="button" className={selectionActionClass} disabled={pending || checkingSide}
			onClick={() => void activate(true)}>{t("sideChat.addToSideChat")}</button> : null}
		{error ? <span role="alert" className="max-w-full px-2 text-xs text-destructive break-words">{error}</span> : null}
	</div>;
}
