import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ImagePlus, Loader2 } from "lucide-react";
import { useCloudCp } from "../hooks/useCloudCp";
import { useFileAttachments, type FileAttachment } from "../hooks/useFileAttachments";
import { CLOUD_IMAGE_LIMITS, uploadCloudAttachments } from "../lib/cloud-attachments";
import type { WorkspaceSession } from "../types/workspace";

export function CloudTerminalAttachments({
	session,
	disabled,
	pastedFiles,
	onInsert,
}: {
	session: WorkspaceSession;
	disabled: boolean;
	pastedFiles?: File[];
	onInsert: (paths: string[], epoch: number) => boolean;
}) {
	const { t } = useTranslation();
	const { client, baseUrl, userId } = useCloudCp();
	const picker = useRef<HTMLInputElement>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();
	const inFlight = useRef(false);
	const current = useRef({ session, disabled, onInsert });
	current.current = { session, disabled, onInsert };
	const uploadFiles = useCallback(
		(files: FileAttachment[]) => {
			if (!session.cloud) throw new Error(t("terminal.images.contextUnavailable"));
			return uploadCloudAttachments(
				client,
				baseUrl,
				session.cloud.orgId,
				session.workspaceId ?? "",
				session.id,
				files,
			);
		},
		[client, baseUrl, session, t],
	);
	const attachments = useFileAttachments({
		uploadFiles,
		limits: CLOUD_IMAGE_LIMITS,
		initialKey: `cloud:${baseUrl}:${userId}:${session.cloud?.orgId}:${session.id}:tui`,
	});
	const prepare = async (files?: File[]) => {
		if (inFlight.current || disabled || !session.cloud) return;
		inFlight.current = true;
		setBusy(true);
		setError(undefined);
		const owner = session.id;
		try {
			if (files) await attachments.addFiles(files);
			await attachments.toSettledPayload();
			const ids = attachments.getAttachments().flatMap((a) => (a.attachmentId ? [a.attachmentId] : []));
			if (ids.length === 0) throw new Error(t("terminal.images.selectFirst"));
			const prepared = await client.materializeAttachments(session.cloud.orgId, owner, ids);
			if (
				current.current.session.id !== owner ||
				current.current.disabled ||
				!current.current.onInsert(prepared.paths, prepared.epoch)
			)
				throw new Error(t("terminal.images.changed"));
			attachments.clear();
		} catch (error) {
			setError(error instanceof Error ? error.message : t("terminal.images.failed"));
		} finally {
			inFlight.current = false;
			setBusy(false);
		}
	};
	const prepareRef = useRef(prepare);
	prepareRef.current = prepare;
	useEffect(() => {
		if (pastedFiles?.length) void prepareRef.current(pastedFiles);
	}, [pastedFiles]);
	return (
		<div className="flex items-center gap-2 border-b border-border px-2 py-1 text-xs text-muted-foreground">
			<input
				ref={picker}
				type="file"
				multiple
				accept="image/png,image/jpeg,image/webp,image/gif,image/bmp"
				className="hidden"
				onChange={(e) => {
					const files = Array.from(e.target.files ?? []);
					e.target.value = "";
					void prepare(files);
				}}
			/>
			<button
				type="button"
				disabled={disabled || busy}
				aria-label={t("terminal.images.attachAria")}
				className="flex items-center gap-1 disabled:opacity-50"
				onClick={() => picker.current?.click()}
			>
				{busy ? <Loader2 className="size-3 animate-spin" /> : <ImagePlus className="size-3" />}
				{t("terminal.images.attach")}
			</button>
			{busy ? <span>{t("terminal.images.preparing")}</span> : <span>{t("terminal.images.hint")}</span>}
			{(error || attachments.error) && (
				<>
					<span role="alert" className="text-destructive">
						{error ?? attachments.error}
					</span>
					<button disabled={disabled || busy} onClick={() => void prepare()}>
						{t("files.retry")}
					</button>
					<button
						disabled={busy}
						onClick={() => {
							attachments.clear();
							setError(undefined);
						}}
					>
						{t("terminal.images.clear")}
					</button>
				</>
			)}
		</div>
	);
}
