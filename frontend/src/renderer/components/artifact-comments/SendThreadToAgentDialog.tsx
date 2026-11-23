import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSendToSessionWorker } from "../../hooks/useSendToSessionWorker";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { useUiStore } from "../../stores/ui-store";
import type { WorkspaceSession } from "../../types/workspace";
import { ConfirmDialog } from "../ConfirmDialog";
import { Label } from "../ui/label";
import { formatArtifactCommentMessage, MAX_ARTIFACT_COMMENT_NOTE, type ArtifactCommentThread } from "./contract";

/** Where Send to agent goes: the session that made the file. Present only when the app knows it. */
export type ArtifactCommentsSendTarget = {
	session: WorkspaceSession;
	hostId?: string;
	/** The file's path in the session's artifacts, as the agent knows it. */
	path: string;
	name: string;
};

/**
 * Sends one thread to the session that made the file, as the owner's
 * message. The target is fixed; the owner can add a note. The thread is
 * marked sent only after the message went through; a failure keeps the
 * dialog open with the error.
 */
export function SendThreadToAgentDialog({ markSent, onOpenChange, open, target, thread, version }: {
	markSent: (threadId: string) => Promise<unknown>;
	onOpenChange: (open: boolean) => void;
	open: boolean;
	target: ArtifactCommentsSendTarget;
	thread: ArtifactCommentThread;
	version: number;
}) {
	const { t } = useTranslation();
	const send = useSendToSessionWorker(target.session, target.hostId);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const navigateToSession = useNavigateToSession();
	const [note, setNote] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	useEffect(() => {
		if (!open) {
			setNote("");
			setError(null);
		}
	}, [open]);
	const sessionName = target.session.title || target.session.id;

	const confirm = async () => {
		setBusy(true);
		setError(null);
		try {
			await send(formatArtifactCommentMessage(thread, { path: target.path, version }, note), t("artifactComments.sendError"));
		} catch (cause) {
			setError(cause instanceof Error && cause.message ? cause.message : t("artifactComments.sendError"));
			setBusy(false);
			return;
		}
		// The message is out; a failed mark only leaves the thread without its label.
		await markSent(thread.id).catch(() => undefined);
		setBusy(false);
		onOpenChange(false);
		showGlobalToast(t("artifactComments.sentToast", { session: sessionName }), undefined, {
			tone: "success",
			action: {
				label: t("artifactComments.openChat"),
				run: () => navigateToSession(target.session.workspaceId, target.session.id, target.hostId),
			},
		});
	};

	return (
		<ConfirmDialog
			busy={busy}
			busyLabel={t("artifactComments.sending")}
			confirmLabel={t("artifactComments.sendToAgent")}
			description={t("artifactComments.sendDescription")}
			error={error}
			onConfirm={() => void confirm()}
			onOpenChange={onOpenChange}
			open={open}
			title={t("artifactComments.sendToAgent")}
		>
			<dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
				<dt className="text-muted-foreground">{t("artifactComments.sendSession")}</dt>
				<dd className="min-w-0 truncate text-foreground" data-testid="send-to-agent-session">{sessionName}</dd>
				<dt className="text-muted-foreground">{t("artifactComments.sendFile")}</dt>
				<dd className="min-w-0 truncate text-foreground">{t("artifactComments.sendFileVersion", { name: target.name, version })}</dd>
			</dl>
			<ul aria-label={t("artifactComments.title")} className="board-scrollbar flex max-h-40 flex-col gap-1.5 overflow-y-auto rounded-md border border-border p-2 text-xs">
				{thread.comments.map((comment) => (
					<li className="whitespace-pre-wrap break-words text-foreground" key={comment.id}>
						<span className="font-medium">{comment.author?.name || t("artifactComments.formerUser")}: </span>
						{comment.deleted ? <span className="text-muted-foreground">{t("artifactComments.deleted")}</span> : comment.body}
					</li>
				))}
			</ul>
			<div className="flex flex-col gap-1.5">
				<Label htmlFor={`send-note-${thread.id}`}>{t("artifactComments.sendNote")}</Label>
				<textarea
					className="board-scrollbar min-h-16 w-full resize-none rounded-md border border-transparent bg-input/50 px-3 py-2 text-sm text-foreground outline-none placeholder:text-muted-foreground disabled:opacity-60"
					disabled={busy}
					id={`send-note-${thread.id}`}
					maxLength={MAX_ARTIFACT_COMMENT_NOTE}
					onChange={(event) => setNote(event.target.value)}
					rows={3}
					value={note}
				/>
			</div>
		</ConfirmDialog>
	);
}
