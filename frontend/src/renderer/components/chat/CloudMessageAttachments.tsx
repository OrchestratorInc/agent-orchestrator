/**
 * Files a cloud chat message carries, shown under the message: images in
 * place (opening full size like any chat image), PDFs as their first page
 * (opening the whole document), and any other file as a chip that saves it.
 * The bytes come from the control plane through the main process, so they are
 * shown as data URLs or read straight into the PDF viewer.
 */

import { createContext, useContext, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { cloudAttachmentReference, fetchCloudAttachment, type CloudAttachment } from "../../lib/cloud-attachments";
import { useSentAttachmentPreview } from "../../lib/sent-attachment-previews";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { PdfPreview } from "../PdfPreview";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Pressable } from "../ui/pressable";
import { ChatImage, ChatImageAttachmentRow } from "./ChatImage";
import { ATTACHMENT_TILE_CLASS, ATTACHMENT_TILE_OVERLAY_PILL_CLASS, ATTACHMENT_TILE_PILL_CLASS, attachmentTypeLabel } from "./messageAttachments";
import { AttachmentFileCard } from "./StagedAttachmentItems";

type CloudAttachmentSource = { baseUrl: string; orgId: string; sessionId: string };

const CloudAttachmentSourceContext = createContext<CloudAttachmentSource | undefined>(undefined);

/** Where a cloud chat's attachments are read from. */
export function CloudAttachmentSourceProvider({ source, children }: { source: CloudAttachmentSource | undefined; children: ReactNode }) {
	return <CloudAttachmentSourceContext.Provider value={source}>{children}</CloudAttachmentSourceContext.Provider>;
}

/** An SVG stays a file chip; it is never rendered as an image. */
function rendersAsImage(attachment: CloudAttachment): boolean {
	return attachment.contentType.startsWith("image/") && attachment.contentType !== "image/svg+xml";
}

function useAttachmentBytes(source: CloudAttachmentSource | undefined, attachment: CloudAttachment, enabled = true) {
	return useQuery({
		queryKey: ["cloud-attachment", source?.orgId, source?.sessionId, attachment.id],
		enabled: Boolean(source) && enabled,
		// An attachment never changes once uploaded.
		staleTime: Infinity,
		gcTime: 10 * 60_000,
		retry: 1,
		queryFn: async () => {
			if (!source) throw new Error("Cloud attachment source is unavailable.");
			const blob = await fetchCloudAttachment(source.baseUrl, source.orgId, source.sessionId, attachment);
			return new Uint8Array(await blob.arrayBuffer());
		},
	});
}

function dataUrl(bytes: Uint8Array, contentType: string): string {
	let binary = "";
	for (let index = 0; index < bytes.length; index += 0x8000) {
		binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
	}
	return `data:${contentType};base64,${btoa(binary)}`;
}

function CloudImageAttachment({ source, attachment }: { source: CloudAttachmentSource | undefined; attachment: CloudAttachment }) {
	// A file this person just sent is still in memory: shown from there, not read
	// back, unless that copy fails to draw.
	const local = useSentAttachmentPreview(cloudAttachmentReference(attachment));
	const bytes = useAttachmentBytes(source, attachment, !local.url);
	return <ChatImage src={local.url ?? (bytes.data ? dataUrl(bytes.data, attachment.contentType) : undefined)} alt={attachment.fileName} onError={local.onError} />;
}

function CloudPdfAttachment({ source, attachment }: { source: CloudAttachmentSource | undefined; attachment: CloudAttachment }) {
	const [open, setOpen] = useState(false);
	const [failed, setFailed] = useState(false);
	const { url: local, onError: onLocalError } = useSentAttachmentPreview(cloudAttachmentReference(attachment));
	const bytes = useAttachmentBytes(source, attachment, !local);
	// The PDF viewer takes ownership of the bytes it is given, so each viewer
	// gets a copy and the cached bytes stay readable for the next one.
	const load = async () => {
		if (local) {
			try {
				return new Uint8Array(await (await fetch(local)).arrayBuffer());
			} catch {
				// The local copy is gone or blocked: let it go, and read the control plane's copy below.
				onLocalError?.();
			}
		}
		if (bytes.data) return bytes.data.slice();
		const result = await bytes.refetch();
		if (!result.data) throw result.error ?? new Error("The PDF could not be read.");
		return result.data.slice();
	};
	if (failed) return <CloudFileAttachment source={source} attachment={attachment} />;
	return (
		<>
			<Pressable
				onClick={() => setOpen(true)}
				title={attachment.fileName}
				className={cn(ATTACHMENT_TILE_CLASS, "focus-visible:ring-2 focus-visible:ring-ring")}
			>
				<AttachmentFileCard name={attachment.fileName} bytes={attachment.size} />
				<span inert className="pointer-events-none absolute inset-0 flex flex-col overflow-hidden">
					<PdfPreview cover load={load} src={`cloud-attachment:${attachment.id}`} path={attachment.fileName} onError={() => setFailed(true)} />
				</span>
				<span className={cn(ATTACHMENT_TILE_PILL_CLASS, ATTACHMENT_TILE_OVERLAY_PILL_CLASS)}>{attachmentTypeLabel(attachment.fileName) ?? "PDF"}</span>
			</Pressable>
			<Dialog open={open} onOpenChange={setOpen}>
				<DialogContent className="flex h-[85vh] max-w-4xl flex-col gap-2 p-3">
					<DialogTitle className="truncate text-sm">{attachment.fileName}</DialogTitle>
					<div className="min-h-0 flex-1 overflow-hidden">
						<PdfPreview load={load} src={`cloud-attachment:${attachment.id}`} path={attachment.fileName} onError={() => setFailed(true)} />
					</div>
				</DialogContent>
			</Dialog>
		</>
	);
}

function CloudFileAttachment({ source, attachment }: { source: CloudAttachmentSource | undefined; attachment: CloudAttachment }) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [saving, setSaving] = useState(false);
	const save = async () => {
		if (!source) return;
		setSaving(true);
		try {
			const blob = await fetchCloudAttachment(source.baseUrl, source.orgId, source.sessionId, attachment);
			await aoBridge.chatImage.save({ name: attachment.fileName, bytes: new Uint8Array(await blob.arrayBuffer()) });
		} catch (error) {
			showGlobalToast(t("chat.attachment.saveFailed"), error instanceof Error ? error.message : undefined, "error");
		} finally {
			setSaving(false);
		}
	};
	return (
		<Pressable
			onClick={() => void save()}
			disabled={!source || saving}
			title={t("chat.attachment.save", { name: attachment.fileName })}
			className={cn(ATTACHMENT_TILE_CLASS, "transition-colors hover:bg-interactive-active focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60")}
		>
			<AttachmentFileCard name={attachment.fileName} bytes={attachment.size} />
		</Pressable>
	);
}

export function CloudMessageAttachments({ attachments, className }: { attachments: CloudAttachment[] | undefined; className?: string }) {
	const { t } = useTranslation();
	const source = useContext(CloudAttachmentSourceContext);
	if (!attachments?.length) return null;
	return (
		<ChatImageAttachmentRow>
			<ul aria-label={t("chat.attachment.list")} className={cn("flex max-w-full flex-wrap gap-2", className)}>
				{attachments.map((attachment) => (
					<li key={attachment.id} className="max-w-full">
						{rendersAsImage(attachment) ? (
							<CloudImageAttachment source={source} attachment={attachment} />
						) : attachment.contentType === "application/pdf" ? (
							<CloudPdfAttachment source={source} attachment={attachment} />
						) : (
							<CloudFileAttachment source={source} attachment={attachment} />
						)}
					</li>
				))}
			</ul>
		</ChatImageAttachmentRow>
	);
}
