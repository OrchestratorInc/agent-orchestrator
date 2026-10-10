import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CliLinkStatus, CliLinkView } from "../../../shared/cli-link";
import { aoBridge } from "../../lib/bridge";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";

export function CliLinkSettings({ enabled }: { enabled: boolean }) {
	useEffect(() => {
		void aoBridge.cliLink.setEnabled(enabled);
	}, [enabled]);
	if (!enabled) return null;
	return <CliLinkSettingsRow />;
}

function CliLinkSettingsRow() {
	const { t } = useTranslation();
	const [view, setView] = useState<CliLinkView | null>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [confirm, setConfirm] = useState<"link" | "unlink" | null>(null);

	const refresh = () => {
		setBusy(true);
		return aoBridge.cliLink.inspect().then((next) => {
			setView(next);
			if (!next.ok) setError(next.message);
		}).catch((cause: unknown) => {
			setError(cause instanceof Error ? cause.message : t("settings.cliLink.loading"));
		}).finally(() => setBusy(false));
	};

	useEffect(() => {
		void refresh();
	}, []);

	const status: CliLinkStatus | null = view?.ok ? view.status : null;
	const foreign = status?.current.kind === "foreign";
	const linked = status?.current.kind === "linked";
	const canLink = Boolean(status && !foreign && status.proposedTarget);
	const canUnlink = status?.current.kind === "linked" || status?.current.kind === "stale";
	const detail = describeStatus((key, options) => String(t(key as never, options as never)), view, error);

	return (
		<div className="flex w-full flex-col">
			<SettingsRow label={t("settings.cliLink.label")}>
				<div className="flex items-center gap-1.5">
					{canUnlink ? (
						<Button type="button" variant="footer" disabled={busy} onClick={() => setConfirm("unlink")}>
							{t("settings.cliLink.unlink")}
						</Button>
					) : null}
					{canLink ? (
						<Button type="button" variant="footer-primary" disabled={busy} onClick={() => setConfirm("link")}>
							{t(linked ? "settings.cliLink.relink" : status?.current.kind === "stale" ? "settings.cliLink.relink" : "settings.cliLink.link")}
						</Button>
					) : null}
				</div>
			</SettingsRow>
			<p className="px-3 pb-2 text-xs leading-relaxed text-muted-foreground" role={error ? "alert" : undefined}>
				{detail}
			</p>
			<ConfirmDialog
				open={confirm !== null}
				title={t(confirm === "unlink" ? "settings.cliLink.confirmUnlinkTitle" : "settings.cliLink.confirmLinkTitle")}
				description={detail}
				confirmLabel={t("settings.cliLink.confirm")}
				cancelLabel={t("settings.cliLink.cancel")}
				busy={busy}
				error={error}
				onOpenChange={(open) => {
					if (!open) setConfirm(null);
				}}
				onConfirm={() => {
					const action = confirm;
					if (!action) return;
					setBusy(true);
					setError(null);
					const op = action === "unlink" ? aoBridge.cliLink.unlink(true) : aoBridge.cliLink.link(true);
					void op.then((result) => {
						if (!result.ok) {
							setError(result.message);
							return;
						}
						setConfirm(null);
						return refresh();
					}).catch((cause: unknown) => {
						setError(cause instanceof Error ? cause.message : t("settings.cliLink.loading"));
					}).finally(() => setBusy(false));
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
	if (!view) return String(t("settings.cliLink.loading"));
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
	const foreign = status.current.kind === "foreign" ? String(t("settings.cliLink.foreign")) : "";
	return [current, proposed, linked, foreign, status.message, error].filter(Boolean).join(" ");
}
