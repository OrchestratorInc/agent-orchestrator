import { uiUsage, useTelemetrySurface } from "../lib/ui-usage-telemetry";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { type GlobalToast as Toast, useUiStore } from "../stores/ui-store";
import { type ToastItem, ToastStack } from "./ui/toast-stack";

const TOAST_DISMISS_MS = 3_500;
const ERROR_TOAST_DISMISS_MS = 8_000;

/** Every app toast, in the one deck in the center panel's top-right corner. */
export function GlobalToast() {
	const { t } = useTranslation();
	const toasts = useUiStore((state) => state.globalToasts);
	const dismissGlobalToast = useUiStore((state) => state.dismissGlobalToast);
	// A deck being read (pointed at or open) does not time out meanwhile.
	const [paused, setPaused] = useState(false);

	if (toasts.length === 0) return null;

	const items: ToastItem[] = toasts.map((toast) => ({
		id: toast.dedupeKey ?? String(toast.nonce),
		title: toast.title,
		help: toast.body,
		tone: toast.tone,
		art: toast.art,
		compact: toast.compact,
		action: toast.action
			? {
					label: toast.action.label,
					busy: toast.action.busy,
					run: () => {
						toast.action?.run();
						if (!toast.action?.keepOpen) dismissGlobalToast(toast.nonce);
					},
				}
			: undefined,
		onOpen: toast.onOpen
			? () => {
					toast.onOpen?.();
					dismissGlobalToast(toast.nonce);
				}
			: undefined,
		onDismiss: () => {
			uiUsage.dismissed("toast");
			dismissGlobalToast(toast.nonce);
		},
	}));

	return (
		<>
			{toasts.map((toast) => <ToastTimer key={toast.nonce} toast={toast} paused={paused} />)}
			<ToastStack items={items} label={t("shortcut.notifications", { defaultValue: "Notifications" })} onPausedChange={setPaused}
				onDismissAll={() => {
					uiUsage.dismissed("toast");
					for (const toast of toasts) dismissGlobalToast(toast.nonce);
				}} />
		</>
	);
}

/** Times one toast out, unless the deck is open. Renders nothing. */
function ToastTimer({ toast, paused }: { toast: Toast; paused: boolean }) {
	const dismissGlobalToast = useUiStore((state) => state.dismissGlobalToast);
	useTelemetrySurface("toast");
	const dismissMs = toast.durationMs ?? (toast.tone === "error" ? ERROR_TOAST_DISMISS_MS : TOAST_DISMISS_MS);
	useEffect(() => {
		if (paused || dismissMs === 0) return;
		const timer = window.setTimeout(() => dismissGlobalToast(toast.nonce), dismissMs);
		return () => window.clearTimeout(timer);
	}, [dismissGlobalToast, dismissMs, paused, toast.nonce]);
	return null;
}
