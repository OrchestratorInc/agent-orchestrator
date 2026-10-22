import { type ReactElement, useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionArtifacts, type WorkspaceSession } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { Checkbox } from "./ui/checkbox";

export type SessionArchiveChoice = { deleteArtifacts: boolean };

// Archiving is reversible — the session moves to the board's Archive section and
// can be restored from there — so this reuses the shared confirm modal without
// the destructive red fill rather than the old inline popover, which read like a
// delete prompt. Callers keep driving `open` themselves: the trigger is rendered
// as-is so repeated taps re-open the confirm instead of toggling it shut.
//
// A local session with artifact files also offers an unchecked "Delete N
// artifacts" box. Checking it turns Confirm destructive, since the delete
// cannot be undone and a restored session comes back without the files.
export function SessionArchiveDialog({
	onConfirm,
	onOpenChange,
	open,
	session,
	trigger,
}: {
	onConfirm: (choice: SessionArchiveChoice) => void;
	onOpenChange: (open: boolean) => void;
	open: boolean;
	session?: WorkspaceSession;
	trigger: ReactElement;
}) {
	const { t } = useTranslation();
	const checkboxId = useId();
	const [deleteArtifacts, setDeleteArtifacts] = useState(false);
	// Every open starts from keeping the artifacts.
	useEffect(() => {
		if (open) setDeleteArtifacts(false);
	}, [open]);
	// A cloud session's teardown is restorable too — the control plane keeps its
	// conversation and work so it can be re-provisioned later.
	const isCloud = session?.cloud !== undefined;
	const body = isCloud ? t("archive.bodyCloud") : t("archive.body");
	const artifactCount = session && !isCloud ? sessionArtifacts(session).length : 0;
	const deleting = artifactCount > 0 && deleteArtifacts;
	return (
		<>
			{trigger}
			<ConfirmDialog
				cancelLabel={t("common.no")}
				confirmAriaLabel={t("archive.confirmAria")}
				confirmLabel={t("confirm.confirm")}
				description={body}
				destructive={deleting}
				onConfirm={() => onConfirm({ deleteArtifacts: deleting })}
				onOpenChange={onOpenChange}
				open={open}
				title={t("archive.dialog")}
			>
				{artifactCount > 0 ? (
					<div className="flex items-start gap-2">
						<Checkbox
							id={checkboxId}
							className="mt-0.5"
							checked={deleteArtifacts}
							onCheckedChange={(next) => setDeleteArtifacts(next === true)}
						/>
						<div className="min-w-0 text-control leading-5">
							<label htmlFor={checkboxId} className="text-foreground">
								{t("archive.deleteArtifacts", { count: artifactCount })}
							</label>
							<p className="text-settings-muted">{t("archive.deleteArtifactsHint", { count: artifactCount })}</p>
						</div>
					</div>
				) : null}
			</ConfirmDialog>
		</>
	);
}
