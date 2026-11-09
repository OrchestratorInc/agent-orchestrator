/**
 * Files a cloud chat message carries, shown under the message: images in
 * place (opening full size like any chat image), PDFs as their first page
 * (opening the whole document), and any other file as a chip that saves it.
 * The bytes come from the control plane through the main process, so they are
 * shown as data URLs or read straight into the PDF viewer.
 */

import { createContext, useContext, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { FileIcon, FileText } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { fetchCloudAttachment, type CloudAttachment } from "../../lib/cloud-attachments";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { PdfPreview } from "../PdfPreview";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Pressable } from "../ui/pressable";
import { ChatImage, ChatImageAttachmentSet } from "./ChatImage";
import { AttachmentFileText } from "./StagedAttachmentItems";

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
	const bytes = useAttachmentBytes(source, attachment);
	return <ChatImage src={bytes.data ? dataUrl(bytes.data, attachment.contentType) : undefined} alt={attachment.fileName} />;
}

function CloudPdfAttachment({ source, attachment }: { source: CloudAttachmentSource | undefined; attachment: CloudAttachment }) {
	const [open, setOpen] = useState(false);
	const [failed, setFailed] = useState(false);
	const bytes = useAttachmentBytes(source, attachment);
	// The PDF viewer takes ownership of the bytes it is given, so each viewer
	// gets a copy and the cached bytes stay readable for the next one.
	const load = async () => {
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
				className="flex w-44 flex-col overflow-hidden rounded-md border border-border bg-background text-left transition-colors hover:bg-interactive-hover"
			>
				<div className="h-32 overflow-hidden">
					<PdfPreview firstPageOnly load={load} src={`cloud-attachment:${attachment.id}`} path={attachment.fileName} onError={() => setFailed(true)} />
				</div>
				<span className="flex items-center gap-1.5 border-t border-border px-2 py-1.5 text-xs text-muted-foreground">
					<FileText aria-hidden="true" className="size-3.5 shrink-0" />
					<span className="truncate">{attachment.fileName}</span>
				</span>
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
			className="flex max-w-full items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-interactive-hover disabled:opacity-60"
		>
			<FileIcon aria-hidden="true" className="size-3.5 shrink-0" />
			<AttachmentFileText name={attachment.fileName} bytes={attachment.size} />
		</Pressable>
	);
}

export function CloudMessageAttachments({ attachments, className }: { attachments: CloudAttachment[] | undefined; className?: string }) {
	const { t } = useTranslation();
	const source = useContext(CloudAttachmentSourceContext);
	if (!attachments?.length) return null;
	return (
		<ChatImageAttachmentSet count={attachments.filter(rendersAsImage).length}>
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
		</ChatImageAttachmentSet>
	);
}
