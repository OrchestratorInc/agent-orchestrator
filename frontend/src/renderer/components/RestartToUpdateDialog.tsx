import { useEffect, useRef, useState } from "react";
import { AlertTriangle } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { parseNightlyVersion } from "../lib/build-channel";
import { sessionsAtRiskFromInstall } from "../lib/update-install-risk";
import { useUpdateStatus } from "../hooks/useUpdateStatus";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { DesktopReleaseNotes } from "./DesktopReleaseNotes";
import { displayableUpdateError } from "../../shared/update-error-message";
import { localizedUpdateError } from "../lib/update-error-copy";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

export function RestartToUpdateDialog() {
	const open = useUiStore((state) => state.updateInstallPromptOpen);
	if (!open) return null;
	return <RestartToUpdateDialogBody />;
}

function RestartToUpdateDialogBody() {
	const { t, i18n } = useTranslation();
	const close = useUiStore((state) => state.closeUpdateInstallPrompt);
	const status = useUpdateStatus(undefined, true);
	const workspace = useWorkspaceQuery();

	const [pending, setPending] = useState(false);
	const [targetChanged, setTargetChanged] = useState(false);
	const [failureDetail, setFailureDetail] = useState<string | null>(null);
	const installing = useRef(false);
	const installStartedStatus = useRef(status);
	const mounted = useRef(true);
	const [confirmedBuild, setConfirmedBuild] = useState<{
		version?: string;
		releaseNotes?: string;
	} | null>(null);
	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
		};
	}, []);

	const failureText = failureDetail && status.state === "error" && status.message === failureDetail
		? localizedUpdateError(status.message, status.errorInfo, failureDetail, t)
		: failureDetail;
	const version = confirmedBuild?.version ?? status.staged?.version ?? status.version;
	const releaseNotes = confirmedBuild
		? confirmedBuild.releaseNotes
		: status.staged?.releaseNotes ?? (status.version === version ? status.releaseNotes : undefined);
	const nightly = parseNightlyVersion(version);
	const buildLabel = nightly
		? t("shell.nightlyBuild", {
				version: nightly.base,
				date: new Intl.DateTimeFormat(i18n.resolvedLanguage ?? i18n.language, {
					month: "short",
					day: "numeric",
				}).format(nightly.builtAt),
			})
		: version
			? `v${version}`
			: null;

	const atRisk = sessionsAtRiskFromInstall(
		(workspace.data ?? []).flatMap((project) => project.sessions),
	);

	const hasFailed = failureDetail !== null;
	useEffect(() => {
		if (
			status !== installStartedStatus.current &&
			status.state === "downloaded" && status.staged?.ready !== false &&
			(status.staged?.version ?? status.version) === version && !pending
		) {
			setFailureDetail(null);
		}
	}, [status, pending, version]);

	const retry = async () => {
		if (installing.current) return;
		installing.current = true;
		setPending(true);
		try {
			await aoBridge.updates.relaunch();
		} catch {
			installing.current = false;
			if (mounted.current) setPending(false);
		}
	};

	const confirm = async () => {
		if (installing.current || !version) return;
		installing.current = true;
		installStartedStatus.current = status;
		setConfirmedBuild({ version, releaseNotes });
		setPending(true);
		setFailureDetail(null);
		try {
			const result = await aoBridge.updates.install(version);
			if (result?.state === "confirmation-required") {
				if (mounted.current) {
					setConfirmedBuild({ version: result.version, releaseNotes: result.releaseNotes });
					setTargetChanged(true);
				}
				return;
			}
			if (mounted.current) close();
		} catch (error) {
			if (mounted.current) {
				setFailureDetail(error instanceof Error
					? displayableUpdateError(error.message.replace(/^Error invoking remote method ['"][^'"]+['"]: (?:Error: )?/, "").trim(), "")
					: "");
			}
		} finally {
			installing.current = false;
			if (mounted.current) setPending(false);
		}
	};

	return (
		<Dialog open onOpenChange={(next) => !next && !installing.current && close()}>
			<DialogContent
				className={settingsDialogContentClass}
				data-testid="restart-to-update-dialog"
				showCloseButton={!pending}
				onEscapeKeyDown={(event) => {
					if (installing.current) event.preventDefault();
				}}
				onInteractOutside={(event) => {
					if (installing.current) event.preventDefault();
				}}
			>
				<div className={cn(settingsDialogHeaderClass, "py-4")}>
					<DialogTitle>{t("update.restart.title")}</DialogTitle>
					{buildLabel && <DialogDescription>{buildLabel}</DialogDescription>}
				</div>

				<div className={cn(settingsDialogBodyClass, "gap-3 pt-4")}>
					{(workspace.isError || !workspace.data) && <p role="status">{t("update.restart.unknownWorkers", { defaultValue: "Current worker state could not be confirmed. Installing restarts Orchestrator and may interrupt current tasks." })}</p>}
					{atRisk.length > 0 && (
						<div
							className="rounded-md border border-warning/30 bg-warning/8 px-3 py-2.5"
							data-testid="restart-sessions-warning"
						>
							<p className="flex items-start gap-2 text-xs font-medium leading-5 text-warning">
								<AlertTriangle className="mt-0.5 size-icon-sm shrink-0" aria-hidden="true" />
								<span className="min-w-0">
									{t("update.restart.sessionsTitle", { count: atRisk.length })}
								</span>
							</p>
							<ul className="mt-2 space-y-1 pl-6">
								{atRisk.map((session) => (
									<li key={session.id} className="truncate text-xs leading-4 text-settings-label">
										{session.workspaceName} · {session.title}
									</li>
								))}
							</ul>
							<p className="mt-2 pl-6 text-xs leading-4 text-settings-muted">
								{t("update.restart.sessionsBody")}
							</p>
						</div>
					)}

					<section className="flex min-h-0 flex-col gap-1.5">
						<p className="text-caption font-medium uppercase tracking-wide text-settings-muted">
							{t("update.restart.whatsNew")}
						</p>
						{releaseNotes ? (
							<DesktopReleaseNotes
								notes={releaseNotes}
								textClassName="settings-thin-scrollbar min-h-0 overflow-y-auto text-pretty text-sm leading-5 text-settings-label"
							/>
						) : (
							<p className="text-sm leading-5 text-settings-muted">{t("update.restart.noNotes")}</p>
						)}
					</section>

					{targetChanged && (
						<p role="status" className="text-sm text-settings-label">
							{t("update.restart.targetChanged")}
						</p>
					)}
					{failureDetail !== null && (
						<div role="alert" className="space-y-1 text-sm text-destructive">
							<p>{t("update.restart.prepareFailed")}</p>
							{failureDetail && <p className="whitespace-pre-line break-words">{failureText}</p>}
						</div>
					)}
					{failureDetail === null && (
						<p className="text-xs leading-4 text-settings-muted">{t("update.restart.installsOnQuit")}</p>
					)}
				</div>

				<div className={cn(settingsDialogFooterClass, "py-4")}>
					<Button type="button" variant="secondary" size="sm" onClick={close} disabled={pending}>
						{t("confirm.cancel")}
					</Button>
					<Button
						type="button"
						variant="primary"
						size="sm"
						onClick={hasFailed ? retry : confirm}
						disabled={pending || (!hasFailed && !version)}
					>
						{pending
							? t("update.restart.preparing")
							: hasFailed
								? t("update.restart.retry")
								: t("update.restart.confirm")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
