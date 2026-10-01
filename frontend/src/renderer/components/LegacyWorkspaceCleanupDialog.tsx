import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { useUiStore } from "../stores/ui-store";
import { ConfirmDialog } from "./ConfirmDialog";

const STORAGE_KEY = "ao.legacyWorkspaceCleanupPrompt.dismissed.v1";
const PROMPT_THRESHOLD_BYTES = 1 << 30;

function readDismissed(): boolean {
	try {
		return window.localStorage.getItem(STORAGE_KEY) === "true";
	} catch {
		return false;
	}
}

function formatBytes(bytes: number): string {
	const units = ["B", "KB", "MB", "GB", "TB"];
	let value = Math.max(0, bytes);
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit += 1;
	}
	const digits = value >= 10 || unit === 0 ? 0 : 1;
	return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: digits }).format(value)} ${units[unit]}`;
}

export function LegacyWorkspaceCleanupDialog() {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const apiBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, () => "");
	const [dismissed, setDismissed] = useState(readDismissed);
	const [open, setOpen] = useState(false);
	const [preview, setPreview] = useState<components["schemas"]["CleanupPreviewResponse"] | null>(null);
	const [cleanupPending, setCleanupPending] = useState(false);
	const [cleanupError, setCleanupError] = useState<string | null>(null);

	useEffect(() => {
		if (dismissed || usesPreviewWorkspaceData || !apiBaseUrl) return;
		let cancelled = false;
		void apiClient.GET("/api/v1/sessions/cleanup/preview").then(({ data }) => {
			if (!cancelled && data) setPreview(data);
		});
		return () => {
			cancelled = true;
		};
	}, [apiBaseUrl, dismissed]);

	const finishPrompt = useCallback(() => {
		setOpen(false);
		setDismissed(true);
		try {
			window.localStorage.setItem(STORAGE_KEY, "true");
		} catch {
			// Dismissing this informational prompt still works if storage is unavailable.
		}
	}, []);

	const runCleanup = async () => {
		if (!preview || cleanupPending) return;
		setCleanupPending(true);
		setCleanupError(null);
		try {
			const { data, error } = await apiClient.POST("/api/v1/sessions/cleanup", {
				body: { sessionIds: preview.sessions.map((session) => session.sessionId) },
			});
			if (error || !data) throw new Error(apiErrorMessage(error, t("legacyCleanup.cleanupFailed")));
			finishPrompt();
			showGlobalToast(t("legacyCleanup.completed", {
				cleaned: data.cleaned.length,
				skipped: data.skipped.length,
			}));
		} catch (error) {
			setCleanupError(apiErrorMessage(error, t("legacyCleanup.cleanupFailed")));
		} finally {
			setCleanupPending(false);
		}
	};

	const hasEnoughToPrompt = Boolean(
		preview?.sessions.length && preview.totalBytes >= PROMPT_THRESHOLD_BYTES,
	);
	useEffect(() => {
		if (dismissed || !preview) return;
		if (hasEnoughToPrompt) setOpen(true);
	}, [dismissed, hasEnoughToPrompt, preview]);

	if (!preview || !hasEnoughToPrompt || dismissed) return null;
	const size = formatBytes(preview.totalBytes);
	const description = (
		<div className="space-y-2">
			<p>{t("legacyCleanup.description", { count: preview.sessions.length, size })}</p>
			<p>{t("legacyCleanup.estimateNote")}</p>
			<p>{t("legacyCleanup.ignoredWarning")}</p>
			{preview.incomplete ? <p>{t("legacyCleanup.incomplete")}</p> : null}
		</div>
	);

	return (
		<ConfirmDialog
			busy={cleanupPending}
			cancelLabel={t("legacyCleanup.keep")}
			confirmAriaLabel={t("legacyCleanup.confirmAria")}
			confirmLabel={t("legacyCleanup.confirm")}
			destructive
			description={description}
			error={cleanupError}
			onConfirm={() => void runCleanup()}
			onOpenChange={(nextOpen) => {
				if (!nextOpen) {
					if (!cleanupPending) finishPrompt();
					return;
				}
				setOpen(true);
			}}
			open={open}
			title={t("legacyCleanup.title", { size })}
		/>
	);
}
