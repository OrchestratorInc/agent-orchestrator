import { type ReactElement, useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionArtifacts, type WorkspaceSession } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { Checkbox } from "./ui/checkbox";

export type SessionArchiveChoice = { deleteArtifacts: boolean };

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
	trigger?: ReactElement;
}) {
	const { t } = useTranslation();
	const checkboxId = useId();
	const [deleteArtifacts, setDeleteArtifacts] = useState(false);
	useEffect(() => {
		if (open) setDeleteArtifacts(false);
	}, [open]);
	const isCloud = session?.cloud !== undefined;
	const body = isCloud ? t("archive.bodyCloud") : t("archive.body");
	const artifactCount = session ? sessionArtifacts(session).length : 0;
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
