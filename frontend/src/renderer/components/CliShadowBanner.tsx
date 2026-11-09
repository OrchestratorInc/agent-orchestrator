import { AlertTriangle, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { CliShadowActions, CliShadowDetails, useCliShadowStatus, useCliShadowTitle } from "./CliShadowActions";
import { Button } from "./ui/button";

// Remembers the dismissed path, not a yes/no: a different ao appearing later is
// a new problem and warns again.
const DISMISSED_PATH_KEY = "ao.cli-shadow-dismissed-identity.v2";

function readDismissedPath(): string | null {
	try {
		return window.localStorage?.getItem(DISMISSED_PATH_KEY) ?? null;
	} catch {
		return null;
	}
}

function writeDismissedPath(path: string): void {
	try {
		window.localStorage?.setItem(DISMISSED_PATH_KEY, path);
	} catch {
		// Without storage the dismissal still lasts for this launch.
	}
}

export function CliShadowBanner() {
	const { t } = useTranslation();
	const status = useCliShadowStatus();
	const title = useCliShadowTitle(status);
	const [dismissedPath, setDismissedPath] = useState(readDismissedPath);
	// A matching or newer ao rarely breaks anything, so it stays in Settings. A
	// version that cannot be read may be just as old, so it still warns.
	if (status?.kind !== "shadowed" || (status.relation !== "older" && status.relation !== "different")) return null;
	const dismissalKey = JSON.stringify([status.shadowPath, status.shadowTarget, status.shadowIdentity]);
	if (dismissalKey === dismissedPath) return null;
	return (
		<section
			aria-live="polite"
			data-browser-native-overlay="true"
			className="pointer-events-auto fixed right-3 bottom-3 z-overlay flex w-daemon-failure-toast items-start gap-3 rounded-welcome-panel border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] px-3.5 py-3 text-xs shadow-[var(--shadow-import-modal)]"
			role="status"
		>
			<AlertTriangle className="mt-0.5 size-icon-base shrink-0 text-warning" aria-hidden="true" />
			<div className="min-w-0 flex-1">
				<p className="font-medium text-(--color-text-import-title)">{title}</p>
				<CliShadowActions status={status} surface="toast" />
				<CliShadowDetails status={status} className="mt-1.5 text-[var(--color-text-import-muted)]" />
			</div>
			<Button
				type="button"
				variant="ghost"
				size="icon-sm"
				aria-label={t("cliShadow.dismiss")}
				className="-mt-1 -mr-1 text-[var(--color-text-import-muted)]"
				onClick={() => {
					writeDismissedPath(dismissalKey);
					setDismissedPath(dismissalKey);
				}}
			>
				<X className="size-icon-base" aria-hidden="true" />
			</Button>
		</section>
	);
}
