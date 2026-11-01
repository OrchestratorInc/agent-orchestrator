import { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { Button } from "../ui/button";
import { installView, showsExistingJob, type InstallJob } from "./installState";

const POLL_INTERVAL_MS = 1500;

/**
 * Shows the connector that makes this machine reachable from outside the
 * local network while it is being installed.
 *
 * The daemon downloads its own pinned cloudflared as soon as remote access
 * needs one, so this mostly reports that download. The package-manager route
 * (`brew`, `winget`, or a command to run on Linux, where AO never asks for an
 * administrator password) is offered only after the download failed. The
 * install button remains for a bridge that has not asked for the connector,
 * such as a LAN-only one.
 */
export function InstallCloudflared({ onInstalled }: { onInstalled: () => void }) {
	const { t } = useTranslation();
	const [job, setJob] = useState<InstallJob | undefined>(undefined);
	const [starting, setStarting] = useState(false);
	const [error, setError] = useState<string | undefined>(undefined);
	const pollRef = useRef<number | null>(null);
	const onInstalledRef = useRef(onInstalled);
	onInstalledRef.current = onInstalled;

	const stopPolling = () => {
		if (pollRef.current !== null) {
			window.clearInterval(pollRef.current);
			pollRef.current = null;
		}
	};
	useEffect(() => stopPolling, []);

	const poll = () => {
		stopPolling();
		pollRef.current = window.setInterval(() => {
			void (async () => {
				const { data, error: pollError } = await apiClient.GET("/api/v1/system/install/{target}", {
					params: { path: { target: "cloudflared" } },
				});
				if (pollError || !data) return; // transient — try again next tick
				setJob(data);
				if (data.status === "running") return;
				stopPolling();
				// The caller asks the daemon to re-resolve and start only remote
				// access. Re-enabling here would rotate the pairing password.
				if (data.status === "succeeded") onInstalledRef.current();
			})();
		}, POLL_INTERVAL_MS);
	};

	// Pick up the download the daemon may already be running.
	useEffect(() => {
		let cancelled = false;
		void (async () => {
			const { data } = await apiClient.GET("/api/v1/system/install/{target}", {
				params: { path: { target: "cloudflared" } },
			});
			if (cancelled || !data || !showsExistingJob(data)) return;
			setJob((current) => current ?? data);
			if (data.status === "running") poll();
		})();
		return () => {
			cancelled = true;
		};
	}, []);

	async function start() {
		setStarting(true);
		setError(undefined);
		const { data, error: startError } = await apiClient.POST("/api/v1/system/install/{target}", {
			params: { path: { target: "cloudflared" } },
		});
		setStarting(false);
		if (startError || !data) {
			setError(apiErrorMessage(startError));
			return;
		}
		setJob(data);
		if (data.status === "running") poll();
	}

	const view = installView(job, starting);

	return (
		<div className="mt-2 flex flex-col gap-1.5" data-testid="mobile-install-cloudflared">
			{view.kind === "offer" && (
				<Button type="button" variant="ghost" size="sm" className="self-start text-settings-muted hover:text-settings-label" onClick={() => void start()}>
					{t("mobile.installConnector", "Install cloudflared")}
				</Button>
			)}
			{(view.kind === "running" || view.kind === "downloading") && (
				<p className="flex items-center gap-1.5 text-xs text-settings-muted">
					<Loader2 className="size-3 shrink-0 animate-spin" aria-hidden="true" />
					{view.kind === "downloading"
						? t("mobile.downloadingConnector", "Downloading cloudflared…")
						: t("mobile.installingConnector", "Installing cloudflared…")}
				</p>
			)}
			{view.kind === "fallback" && (
				<>
					<p className="text-xs text-error">{view.reason}</p>
					<Button type="button" variant="ghost" size="sm" className="self-start text-settings-muted hover:text-settings-label" onClick={() => void start()}>
						{t("mobile.installConnectorFallback", "Install with your package manager")}
					</Button>
				</>
			)}
			{view.kind === "manual" && (
				<>
					<p className="text-xs text-settings-muted">
						{view.reason || t("mobile.installConnectorManual", "Run this in a terminal, then try again.")}
					</p>
					<code className="block overflow-x-auto rounded bg-(--color-bg-settings-input) px-2 py-1 text-xs text-settings-label">
						{view.command}
					</code>
					<Button type="button" variant="ghost" size="sm" className="self-start text-settings-muted hover:text-settings-label" onClick={onInstalled}>
						{t("mobile.checkConnectorAgain", "Check again")}
					</Button>
				</>
			)}
			{view.kind === "failed" && <p className="text-xs text-error">{view.reason}</p>}
			{error && <p className="text-xs text-error">{error}</p>}
		</div>
	);
}
