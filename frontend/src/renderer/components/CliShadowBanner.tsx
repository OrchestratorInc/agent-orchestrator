import { AlertTriangle, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CliShadowStatus } from "../../shared/cli-shadow";
import { aoBridge } from "../lib/bridge";
import { Button } from "./ui/button";

// Remembers the dismissed path, not a yes/no: a different ao appearing later is
// a new problem and warns again.
const DISMISSED_PATH_KEY = "ao.cli-shadow-dismissed-path.v1";

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

export function useCliShadowStatus(): CliShadowStatus | null {
	const [status, setStatus] = useState<CliShadowStatus | null>(null);
	useEffect(() => {
		let active = true;
		void (async () => {
			try {
				const next = await aoBridge.cliShadow.inspect();
				if (active) setStatus(next);
			} catch {
				// An advisory check that fails has nothing to tell the user.
			}
		})();
		return () => {
			active = false;
		};
	}, []);
	return status;
}

export function useCliShadowDetail(status: CliShadowStatus | null): string | null {
	const { t } = useTranslation();
	if (status?.kind !== "shadowed") return null;
	return t("cliShadow.detail", {
		shadowPath: status.shadowPath,
		shadowVersion: status.shadowVersion ?? t("cliShadow.versionUnknown"),
		bundledPath: status.bundledPath,
	});
}

export function CliShadowBanner() {
	const { t } = useTranslation();
	const status = useCliShadowStatus();
	const detail = useCliShadowDetail(status);
	const [dismissedPath, setDismissedPath] = useState(readDismissedPath);
	if (status?.kind !== "shadowed" || status.shadowPath === dismissedPath) return null;
	const { shadowPath } = status;
	return (
		<section
			aria-live="polite"
			data-browser-native-overlay="true"
			className="pointer-events-auto fixed right-3 bottom-3 z-overlay flex w-daemon-failure-toast items-start gap-3 rounded-welcome-panel border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] px-3.5 py-3 text-xs shadow-[var(--shadow-import-modal)]"
			role="status"
		>
			<AlertTriangle className="mt-0.5 size-icon-base shrink-0 text-warning" aria-hidden="true" />
			<div className="min-w-0 flex-1">
				<p className="font-medium text-(--color-text-import-title)">{t("cliShadow.title")}</p>
				<p className="mt-0.5 wrap-break-word text-pretty text-[var(--color-text-import-muted)]">{detail}</p>
			</div>
			<Button
				type="button"
				variant="ghost"
				size="icon-sm"
				aria-label={t("cliShadow.dismiss")}
				className="-mt-1 -mr-1 text-[var(--color-text-import-muted)]"
				onClick={() => {
					writeDismissedPath(shadowPath);
					setDismissedPath(shadowPath);
				}}
			>
				<X className="size-icon-base" aria-hidden="true" />
			</Button>
		</section>
	);
}
