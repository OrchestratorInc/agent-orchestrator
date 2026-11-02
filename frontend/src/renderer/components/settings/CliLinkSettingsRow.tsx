import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CliLinkStatus, CliLinkView } from "../../../shared/cli-link";
import { aoBridge } from "../../lib/bridge";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";

export function CliLinkSettings({ enabled }: { enabled: boolean }) {
	const [ready, setReady] = useState(false);
	useEffect(() => {
		let active = true;
		setReady(false);
		void aoBridge.cliLink.setEnabled(enabled).then(() => {
			if (active) setReady(enabled);
		});
		return () => { active = false; };
	}, [enabled]);
	if (!enabled) return null;
	return ready ? <CliLinkSettingsRow /> : null;
}

function CliLinkSettingsRow() {
	const { t } = useTranslation();
	const [view, setView] = useState<CliLinkView | null>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [confirm, setConfirm] = useState<{ action: "link" | "unlink"; target: string | null } | null>(null);

	const refresh = () => {
		setBusy(true);
		return aoBridge.cliLink.inspect().then((next) => {
			setView(next);
			setError(next.ok ? null : next.message);
		}).catch((cause: unknown) => {
			setView(null);
			setError(cause instanceof Error ? cause.message : "Could not inspect the global ao command.");
		}).finally(() => setBusy(false));
	};
	const runAction = async (action: "link" | "unlink", target: string | null, confirmed: boolean) => {
		setBusy(true);
		setError(null);
		try {
			const result = action === "unlink"
				? await aoBridge.cliLink.unlink(confirmed, target)
				: await aoBridge.cliLink.link(confirmed, target);
			if (result.ok) setConfirm(null);
			await refresh();
			if (!result.ok) setError(result.message);
		} catch (cause) {
			await refresh();
			setError(cause instanceof Error ? cause.message : "Could not update the global ao command.");
		} finally {
			setBusy(false);
		}
	};

	useEffect(() => {
		void refresh();
	}, []);

	const status: CliLinkStatus | null = view?.ok ? view.status : null;
	const foreign = status?.current.kind === "foreign";
	const linked = status?.current.kind === "linked";
	const canLink = Boolean(status && !foreign && !linked && !status.pathIssue && status.proposedTarget);
	const canUnlink = status?.current.kind === "linked" || status?.current.kind === "stale";
	const detail = linked && !status?.message && !status?.pathIssue && !error ? t("settings.cliLink.linked") : describeStatus((key, options) => String(t(key as never, options as never)), view, error);

	return (
		<div className="flex w-full flex-col">
			<SettingsRow label={t("settings.cliLink.label")}>
				<div className="flex items-center gap-1.5">
					{canUnlink ? (
						<Button type="button" variant="footer" disabled={busy} onClick={() => {
							if (!status || status.current.kind === "missing") return;
							setError(null);
							setConfirm({ action: "unlink", target: status.current.target });
						}}>
							{t("settings.cliLink.unlink")}
						</Button>
					) : null}
					{canLink ? (
						<Button type="button" variant="footer-primary" disabled={busy} onClick={() => {
							if (!status) return;
							setError(null);
							if (status.current.kind === "missing") void runAction("link", null, false);
							else setConfirm({ action: "link", target: status.current.target });
						}}>
							{t(status?.current.kind === "stale" ? "settings.cliLink.relink" : "settings.cliLink.link")}
						</Button>
					) : null}
				</div>
			</SettingsRow>
			<p className="px-3 pb-2 text-xs leading-relaxed text-muted-foreground" role={error ? "alert" : undefined}>
				{detail}
			</p>
			<ConfirmDialog
				open={confirm !== null}
				title={t(confirm?.action === "unlink" ? "settings.cliLink.confirmUnlinkTitle" : "settings.cliLink.confirmLinkTitle")}
				description={describeStatus((key, options) => String(t(key as never, options as never)), view, null)}
				confirmLabel={t("confirm.confirm")}
				busy={busy}
				error={error}
				onOpenChange={(open) => {
					if (!open) { setConfirm(null); setError(null); }
				}}
				onConfirm={() => {
					if (confirm) void runAction(confirm.action, confirm.target, true);
				}}
			/>
		</div>
	);
}

function describeStatus(
	t: (key: string, options?: Record<string, string>) => string,
	view: CliLinkView | null,
	error: string | null,
): string {
	if (!view) return error || String(t("settings.cliLink.loading"));
	if (!view.ok) return error || view.message;
	const { status } = view;
	const current = status.current.kind === "missing"
		? String(t("settings.cliLink.currentMissing", { path: status.linkPath }))
		: String(t("settings.cliLink.current", { target: status.current.target ?? status.linkPath }));
	const proposed = !status.proposedTarget ? ""
		: status.proposedVersion
			? String(t("settings.cliLink.proposed", { target: status.proposedTarget, version: status.proposedVersion }))
			: String(t("settings.cliLink.proposedUnknown", { target: status.proposedTarget }));
	const linked = status.current.kind === "linked" ? String(t("settings.cliLink.linked", { version: status.current.version })) : "";
	const foreign = status.current.kind === "foreign" && !status.message ? String(t("settings.cliLink.foreign")) : "";
	return [current, proposed, linked, foreign, status.message, error].filter(Boolean).join(" ");
}
