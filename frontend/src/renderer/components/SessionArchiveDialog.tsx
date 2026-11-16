import { type ReactElement, useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionArtifacts, sessionDeliverables, type WorkspaceSession } from "../types/workspace";
import { sessionPublishedCheck, type PublishedCheck } from "../lib/unpublish-artifacts";
import { usePublishedLinkCount } from "../hooks/usePublishedLinkCount";
import { useSettings } from "../hooks/useSettings";
import { useHostShareLinks } from "./ShareArtifactPopover";
import { ConfirmDialog } from "./ConfirmDialog";
import { Checkbox } from "./ui/checkbox";

export type SessionArchiveChoice = { deleteArtifacts: boolean };

// Archiving is reversible — the session moves to the board's Archive section and
// can be restored from there — so this reuses the shared confirm modal without
// the destructive red fill rather than the old inline popover, which read like a
// delete prompt. Callers keep driving `open` themselves: the trigger is rendered
// as-is so repeated taps re-open the confirm instead of toggling it shut.
//
// Put a tooltip inside `trigger`, never around this component: a portal stays a
// React child, so the confirm's focus would open the tooltip above it, and the
// tooltip would take Escape.
//
// A session with artifact files also offers an unchecked "Delete N
// artifacts" box. Checking it turns Confirm destructive, since the delete
// cannot be undone and a restored session comes back without the files. It
// says how many are published: their links stop working with the files.
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
	/** Omitted when a shortcut, not a visible control, opens the confirm. */
	trigger?: ReactElement;
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
	// Counted as every list shows them: registered files, a bundle as one. The
	// delete still takes the whole folder, so its other files are mentioned.
	const deliverables = session ? sessionDeliverables(session) : [];
	const artifactCount = deliverables.length;
	const otherFiles = session ? sessionArtifacts(session).length > deliverables.reduce((sum, artifact) => sum + (artifact.bundle?.fileCount ?? 1), 0) : false;
	const published = sessionPublishedCheck(session);
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
							<p className="text-settings-muted">
								{t("archive.deleteArtifactsHint", { count: artifactCount })}
								{otherFiles ? ` ${t("archive.deleteArtifactsFiles")}` : null}
								{session?.hostId ? <HostPublishedNote session={session} /> : published.lookup.length > 0 ? <LookedUpPublishedNote check={published} /> : <PublishedNote count={published.known} />}
							</p>
						</div>
					</div>
				) : null}
			</ConfirmDialog>
		</>
	);
}

function PublishedNote({ count }: { count: number }) {
	const { t } = useTranslation();
	return count > 0 ? <>{` ${t("archive.deleteArtifactsPublished", { count })}`}</> : null;
}

/**
 * The note for a cloud session, whose files' links are recorded on this
 * computer only and so are looked up; mounted only then, while the confirm is open.
 */
function LookedUpPublishedNote({ check }: { check: PublishedCheck }) {
	const baseUrl = useSettings().settings?.cloudControlPlaneUrl ?? "";
	return <PublishedNote count={usePublishedLinkCount(baseUrl, check, true)} />;
}

/** The note for a remote host's session, whose links only this computer's host links name. */
function HostPublishedNote({ session }: { session: WorkspaceSession }) {
	return <PublishedNote count={sessionPublishedCheck(session, useHostShareLinks()).known} />;
}
