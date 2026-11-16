import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { Button } from "../ui/button";

/** How long "Stopped sharing · Undo" stays up. Access is gone at once, not when it goes. */
export const UNDO_NOTICE_MS = 8_000;

export type UndoOffer = {
	message: string;
	/** Restores what was stopped; absent when it can't be shared again the same way. */
	undo?: () => Promise<unknown>;
};

type Shown = UndoOffer & { id: number; shownAt: number };

/**
 * The Undo offer after stopping a share, for a surface inside a modal (the
 * share dialog, Settings). The global toast sits outside the modal's focus
 * trap, where neither the keyboard nor a screen reader can reach it, so while
 * the surface is up the offer is shown in it. When the surface goes (the
 * dialog closes, the page changes) the offer moves to the global toast for
 * the time it has left; an offer made while the surface is down goes there
 * directly.
 */
export function useUndoNotice(active: boolean) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [shown, setShown] = useState<Shown | null>(null);
	const [failed, setFailed] = useState(false);
	const shownRef = useRef(shown);
	shownRef.current = shown;
	const activeRef = useRef(active);
	activeRef.current = active;
	const ids = useRef(0);

	const toGlobal = useCallback((offer: UndoOffer, durationMs: number) => {
		const undo = offer.undo;
		showGlobalToast(offer.message, undefined, {
			durationMs,
			...(undo
				? {
					action: {
						label: t("share.undo"),
						run: () => {
							void undo().catch(() => showGlobalToast(t("share.undoFailed"), undefined, { tone: "error" }));
						},
					},
				}
				: {}),
		});
	}, [showGlobalToast, t]);
	const toGlobalRef = useRef(toGlobal);
	toGlobalRef.current = toGlobal;

	const show = useCallback((offer: UndoOffer) => {
		setFailed(false);
		if (!activeRef.current) {
			toGlobalRef.current(offer, UNDO_NOTICE_MS);
			return;
		}
		ids.current += 1;
		setShown({ ...offer, id: ids.current, shownAt: Date.now() });
	}, []);
	const dismiss = useCallback(() => setShown(null), []);
	const undo = useCallback(() => {
		const current = shownRef.current;
		setShown(null);
		if (!current?.undo) return;
		void current.undo().catch(() => setFailed(true));
	}, []);
	const clearFailure = useCallback(() => setFailed(false), []);

	// Hands a live offer to the global toast when the surface goes away.
	const handOff = useCallback(() => {
		const current = shownRef.current;
		setShown(null);
		setFailed(false);
		if (!current) return;
		const left = UNDO_NOTICE_MS - (Date.now() - current.shownAt);
		if (left > 0) toGlobalRef.current(current, Math.max(left, 3_000));
	}, []);
	useEffect(() => {
		if (!active) handOff();
	}, [active, handOff]);
	useEffect(() => handOff, [handOff]);

	return { shown, failed, show, dismiss, undo, clearFailure };
}

/**
 * "Stopped sharing with Platform · Undo", in the global toast's look, inside
 * the surface the share was stopped from. Hovering or focusing it holds it.
 */
export function UndoNotice({ notice, onUndo, onDismiss, className }: {
	notice: { id: number; message: string; undo?: unknown } | null;
	onUndo: () => void;
	onDismiss: () => void;
	className?: string;
}) {
	const { t } = useTranslation();
	const [held, setHeld] = useState(false);
	const id = notice?.id;
	useEffect(() => {
		if (id === undefined || held) return;
		const timer = window.setTimeout(onDismiss, UNDO_NOTICE_MS);
		return () => window.clearTimeout(timer);
	}, [held, id, onDismiss]);
	useEffect(() => setHeld(false), [id]);
	if (!notice) return null;
	return (
		<div
			key={notice.id}
			role="status"
			aria-live="polite"
			className={cn(
				"toast-enter flex items-center gap-3 rounded-welcome-panel border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] px-3.5 py-2.5 text-xs shadow-[var(--shadow-import-modal)]",
				className,
			)}
			onMouseEnter={() => setHeld(true)}
			onMouseLeave={() => setHeld(false)}
			onFocus={() => setHeld(true)}
			onBlur={() => setHeld(false)}
			data-undo-notice=""
		>
			<p className="min-w-0 flex-1 wrap-break-word font-medium text-(--color-text-import-title)">{notice.message}</p>
			{notice.undo ? (
				<Button type="button" variant="ghost" size="xs" onClick={onUndo} className="-my-1 font-medium" data-undo-action="">
					{t("share.undo")}
				</Button>
			) : null}
		</div>
	);
}
