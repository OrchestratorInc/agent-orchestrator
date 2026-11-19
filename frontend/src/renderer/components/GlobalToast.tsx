import { useEffect, useState, type ReactNode } from "react";
import { Check, LoaderCircle } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { cancelWaitingMove, useMoveStore, type MoveState } from "../lib/move-runner";
import { type GlobalToast as Toast, useUiStore } from "../stores/ui-store";
import { moveFraction, useMoveStepText } from "./MoveBanner";
import { CloseButton } from "./ui/close-button";
import { Button } from "./ui/button";

const TOAST_DISMISS_MS = 3_500;
const ERROR_TOAST_DISMISS_MS = 8_000;

export function GlobalToast() {
	const toasts = useUiStore((state) => state.globalToasts);
	// Moves no open session shows: their progress lives here instead.
	const { moves, shown } = useMoveStore();
	const background = Object.entries(moves).filter(([key, move]) => (move.phase === "waiting" || move.phase === "moving") && !(shown[key] > 0));

	if (toasts.length === 0 && background.length === 0) return null;

	return (
		<>
			<GlobalToastStack placement="top-center" toasts={toasts.filter((toast) => toast.placement === "top-center")} />
			<GlobalToastStack placement="bottom-center" toasts={toasts.filter((toast) => toast.placement === "bottom-center")} />
			<GlobalToastStack
				placement="bottom-right"
				toasts={toasts.filter((toast) => !toast.placement || toast.placement === "bottom-right")}
				leading={background.map(([key, move]) => <MoveToast key={key} moveKey={key} move={move} />)}
			/>
		</>
	);
}

function GlobalToastStack({ placement, toasts, leading = [] }: { placement: NonNullable<Toast["placement"]>; toasts: Toast[]; leading?: ReactNode[] }) {
	if (toasts.length === 0 && leading.length === 0) return null;
	return (
		<div
			className={`pointer-events-none fixed z-[calc(var(--z-overlay)+1)] flex max-w-[calc(100vw-24px)] gap-2 ${
				placement === "top-center" ? "w-[min(360px,calc(100vw-24px))] items-stretch left-1/2 top-12 max-h-[184px] -translate-x-1/2 flex-col overflow-y-auto overscroll-contain" : placement === "bottom-center" ? "left-1/2 bottom-3 -translate-x-1/2 w-max items-center flex-col-reverse" : "w-[min(360px,calc(100vw-24px))] items-stretch right-3 bottom-3 flex-col-reverse"
			}`}
			data-browser-native-overlay="true"
			data-state="open"
		>
			{leading}
			{toasts.slice().reverse().map((toast) => <GlobalToastItem key={toast.nonce} toast={toast} />)}
		</div>
	);
}

/** A move running while its session is not on screen: which session, where to, and how far. */
function MoveToast({ moveKey: key, move }: { moveKey: string; move: MoveState }) {
	const { t } = useTranslation();
	const step = useMoveStepText(move);
	const fraction = moveFraction(move);
	const moving = t("moveSession.moving", { host: move.targetLabel });
	return (
		<section
			role="status"
			aria-live="polite"
			className="toast-enter pointer-events-auto flex items-start gap-3 rounded-welcome-panel border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] px-3.5 py-3 text-xs shadow-[var(--shadow-import-modal)]"
			data-move-toast=""
		>
			<LoaderCircle aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 animate-spin text-[var(--color-status-working)]" />
			<div className="min-w-0 flex-1">
				<p className="truncate font-medium text-(--color-text-import-title)">{move.title ?? moving}</p>
				<p className="mt-0.5 truncate text-[var(--color-text-import-muted)]">{move.title ? `${moving} · ${step}` : step}</p>
				{fraction === undefined ? null : (
					<div className="mt-2 h-0.5 overflow-hidden rounded-full bg-muted">
						<div className="h-full bg-[var(--color-status-working)] transition-[width] duration-150" style={{ width: `${fraction * 100}%` }} />
					</div>
				)}
			</div>
			{move.phase === "waiting" ? (
				<Button type="button" variant="ghost" size="xs" className="-my-1 font-medium" onClick={() => cancelWaitingMove(key)}>
					{t("moveSession.cancelMove")}
				</Button>
			) : null}
		</section>
	);
}

function GlobalToastItem({ toast }: { toast: Toast }) {
	const { t } = useTranslation();
	const [exiting, setExiting] = useState(false);
	const dismissGlobalToast = useUiStore((state) => state.dismissGlobalToast);
	const isError = toast.tone === "error";
	const isSuccess = toast.tone === "success";
	const dismissMs = toast.durationMs ?? (isError ? ERROR_TOAST_DISMISS_MS : TOAST_DISMISS_MS);
	// Hovering or focusing a toast pauses its timer so it can be read and acted on.
	const [paused, setPaused] = useState(false);

	useEffect(() => {
		if (paused || dismissMs === 0) return;
		const exitTimer = window.setTimeout(() => setExiting(true), Math.max(0, dismissMs - 180));
		const dismissTimer = window.setTimeout(() => dismissGlobalToast(toast.nonce), dismissMs);
		return () => {
			window.clearTimeout(exitTimer);
			window.clearTimeout(dismissTimer);
		};
	}, [dismissGlobalToast, dismissMs, paused, toast.nonce]);

	return (
		<section
			aria-live={isError ? "assertive" : "polite"}
			className={`${exiting ? "toast-exit" : "toast-enter"} pointer-events-auto flex rounded-welcome-panel ${isSuccess ? "items-center gap-2 px-3 py-2" : "items-start gap-3 px-3.5 py-3"} text-xs shadow-[var(--shadow-import-modal)] ${
				isError ? "border border-destructive/40 bg-destructive/10" : "border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)]"
			}`}
			role={isError ? "alert" : "status"}
			data-global-toast=""
			onMouseEnter={() => setPaused(true)}
			onMouseLeave={() => setPaused(false)}
			onFocus={() => setPaused(true)}
			onBlur={() => setPaused(false)}
		>
			{isSuccess && <Check aria-hidden="true" className="size-4 shrink-0 text-green-500" />}
			<div className="min-w-0 flex-1">
				<p className={`wrap-break-word font-medium ${isError ? "text-destructive" : "text-(--color-text-import-title)"}`}>{toast.title}</p>
				{toast.body ? <p className="mt-0.5 wrap-break-word text-pretty text-[var(--color-text-import-muted)]">{toast.body}</p> : null}
			</div>
			{toast.action ? (
				<Button
					type="button"
					variant="ghost"
					size="xs"
					onClick={() => {
						toast.action?.run();
						dismissGlobalToast(toast.nonce);
					}}
					className="-my-1 font-medium"
					data-toast-action=""
				>
					{toast.action.label}
				</Button>
			) : null}
			{toast.dismissible ? (
				<CloseButton
					aria-label={t("shortcut.dismissNotification")}
					onClick={() => dismissGlobalToast(toast.nonce)}
					size="icon-xs"
				/>
			) : null}
		</section>
	);
}
