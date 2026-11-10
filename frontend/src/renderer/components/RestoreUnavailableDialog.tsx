import * as Dialog from "@radix-ui/react-dialog";
import { Loader2 } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { isOrchestratorSession } from "../types/workspace";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import {
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

type RestoreUnavailableDialogProps = {
	open: boolean;
	session: WorkspaceSession;
	hostId?: string;
	onOpenChange: (open: boolean) => void;
	onRecreated: (newOrchestratorId: string) => void;
};

export function RestoreUnavailableDialog({ open, session, hostId, onOpenChange, onRecreated }: RestoreUnavailableDialogProps) {
	const { t } = useTranslation();
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const orchestrator = isOrchestratorSession(session);

	const recreate = async () => {
		setBusy(true);
		setError(undefined);
		try {
			const id = hostId
				? await openRemoteOrchestrator(hostId, session.workspaceId, undefined, undefined, true, "restore_dialog")
				: await spawnOrchestrator(session.workspaceId, "restore_dialog", true);
			onOpenChange(false);
			onRecreated(id);
		} catch (err) {
			setError(err instanceof Error ? err.message : t("restoreUnavailable.createFailed"));
		} finally {
			setBusy(false);
		}
	};

	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Overlay className="dialog-overlay data-[state=open]:animate-overlay-in" />
				<Dialog.Content
					className={`${settingsDialogContentClass} fixed left-1/2 top-1/2 w-dialog-md -translate-x-1/2 -translate-y-1/2 data-[state=open]:animate-modal-in`}
				>
					<CloseButton floating aria-label={t("common.close")} disabled={busy} onClick={() => onOpenChange(false)} />
					<div className={settingsDialogHeaderClass}>
						<Dialog.Title className="settings-dialog-title">{t("restoreUnavailable.title")}</Dialog.Title>
						<Dialog.Description className="text-control text-settings-muted">
							{orchestrator ? t("restoreUnavailable.orchestratorBody") : t("restoreUnavailable.sessionBody")}
						</Dialog.Description>
					</div>
					{error ? (
						<div className={settingsDialogBodyClass}>
							<p className="text-xs text-destructive">{error}</p>
						</div>
					) : null}
					<div className={settingsDialogFooterClass}>
						<Button type="button" variant="secondary" onClick={() => onOpenChange(false)} disabled={busy}>
							{orchestrator ? t("confirm.cancel") : t("restoreUnavailable.close")}
						</Button>
						{orchestrator ? (
							<Button
								type="button"
								variant="primary"
								onClick={recreate}
								disabled={busy}
							>
								{busy ? <Loader2 className="size-icon-base animate-spin" aria-hidden="true" /> : null}
								{t("restoreUnavailable.createOrchestrator")}
							</Button>
						) : null}
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
