import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { ArrowUp, File, Loader2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { ConversationContentSummary } from "../../types/conversation";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { ConversationContentItems } from "./ConversationContentItems";
import {
	attachmentName,
	attachmentURL,
	IMAGE_ATTACHMENT_PATH,
	stagedAttachmentParts,
	withAttachmentReferences,
} from "./messageAttachments";

export interface HumanMessageEditorProps {
	/** The full prompt text, including AO's attached-files block when the message had one. */
	text: string;
	content: ConversationContentSummary[];
	sessionId: string;
	apiBaseUrl: string | null;
	pending: boolean;
	/** Durable unresolved delivery: lock edits, but allow explicit safe recovery. */
	locked?: boolean;
	recoveryLabel?: string;
	onAbandonRecovery?: () => void;
	sendBlocked?: boolean;
	busy: boolean;
	error?: string;
	onDraftChange?: (text: string) => void;
	onCancel: () => void;
	onSend: (text: string) => Promise<unknown> | void;
}

export function HumanMessageEditor({
	text,
	content,
	sessionId,
	apiBaseUrl,
	pending,
	locked = false,
	recoveryLabel,
	onAbandonRecovery,
	sendBlocked = false,
	busy,
	error,
	onDraftChange,
	onCancel,
	onSend,
}: HumanMessageEditorProps) {
	const { t } = useTranslation();
	// The textarea holds only what the person wrote. AO's attached-files block is
	// shown as chips and written back on every publish, so the agent still gets it.
	const [draft, setDraft] = useState(() => stagedAttachmentParts(text).body);
	const [attachments, setAttachments] = useState(() => stagedAttachmentParts(text).attachments);
	const published = useRef<string | undefined>(undefined);
	const textarea = useRef<HTMLTextAreaElement>(null);
	const branchNoteId = useId();
	const composed = withAttachmentReferences(draft.trimEnd(), attachments);
	const sendDisabled =
		pending || sendBlocked || busy || composed.trim().length === 0 || (locked && !recoveryLabel);
	const busyMessage = busy ? t("chat.edit.stopCurrentTurn") : undefined;
	const images = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	// A native image block is the same picture as its staged image chip; listing
	// both would show it twice.
	const otherContent = images.length > 0 ? content.filter((item) => item.type !== "image") : content;

	useEffect(() => {
		// React can preserve this editor while its Chat surface is hidden. If another
		// committed surface advances the durable revision in that interval, the parent
		// restores the newer text when this surface reconnects and it must replace the
		// stale local seed. Text this editor published itself is skipped, so splitting
		// it again cannot drop whitespace the person is still typing.
		if (text === published.current) return;
		const parts = stagedAttachmentParts(text);
		setDraft(parts.body);
		setAttachments(parts.attachments);
	}, [text]);

	function publish(nextDraft: string, nextAttachments: string[]) {
		const next = withAttachmentReferences(nextDraft, nextAttachments);
		published.current = next;
		onDraftChange?.(next);
	}

	function removeAttachment(path: string) {
		const next = attachments.filter((candidate) => candidate !== path);
		setAttachments(next);
		publish(draft, next);
		textarea.current?.focus();
	}

	useEffect(() => {
		const node = textarea.current;
		if (!node) return;
		node.style.height = "0px";
		node.style.height = `${Math.min(node.scrollHeight, 224)}px`;
	}, [draft]);

	function submit() {
		if (sendDisabled) return;
		void Promise.resolve(onSend(composed)).catch(() => {});
	}

	function onKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
		if (event.key === "Escape") {
			event.preventDefault();
			onCancel();
			return;
		}
		if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			submit();
		}
	}

	return (
		<div className="cursor-chat-composer relative flex w-full max-w-3xl flex-col gap-1.5 px-4 py-3">
		{attachments.length > 0 ? (
			<ul aria-label={t("chat.attachment.list")} className="flex flex-wrap items-center gap-1.5">
				{attachments.map((path) => {
					const image = IMAGE_ATTACHMENT_PATH.test(path);
					const label = image ? t("chat.image.numbered", { index: images.indexOf(path) + 1 }) : attachmentName(path);
					return (
						<li
							key={path}
							title={label}
							className={cn("flex items-center gap-1.5 rounded bg-interactive-hover py-0.5 pl-0.5", image ? "pr-1.5" : "pr-1")}
						>
							{image && apiBaseUrl !== null ? (
								<img src={attachmentURL(apiBaseUrl, sessionId, path)} alt="" className="size-6 rounded-sm object-cover" />
							) : (
								<div className="flex size-6 items-center justify-center rounded-sm bg-surface">
									<File aria-hidden="true" className="size-3.5 text-muted-foreground" />
								</div>
							)}
							<span className="max-w-[120px] truncate text-xs text-muted-foreground">{label}</span>
							{/* An edit always resends the original image data, so an image
							    chip has no remove: dropping its path would not unsend it. */}
							{image ? null : (
								<Button
									type="button"
									variant="destructive-ghost"
									size="icon-xs"
									onClick={() => removeAttachment(path)}
									disabled={pending || locked}
									aria-label={t("chat.attachment.remove", { name: label })}
								>
									<X aria-hidden="true" className="size-3" />
								</Button>
							)}
						</li>
					);
				})}
			</ul>
		) : null}
		<textarea
			ref={textarea}
			value={draft}
			onChange={(event) => {
				setDraft(event.target.value);
				publish(event.target.value, attachments);
			}}
			onKeyDown={onKeyDown}
			aria-label={t("chat.edit.label")}
			aria-describedby={error || busyMessage ? undefined : branchNoteId}
			disabled={pending || locked}
			autoFocus
			rows={2}
			className="chat-composer-scrollbar max-h-56 min-h-[3.25rem] w-full resize-none overflow-y-auto overscroll-contain bg-transparent px-0 py-1 text-sm leading-relaxed text-foreground outline-none"
		/>
		<ConversationContentItems
			content={otherContent}
			ariaLabel={t("chat.edit.preservedContent")}
			imageLabel={t("chat.edit.image")}
			className="mt-2"
		/>
		<div className="mt-2 flex min-h-7 items-center justify-end gap-1.5">
			{error ? (
				<span role="alert" className="mr-auto text-xs text-destructive">
					{error}
				</span>
			) : busyMessage ? (
				<span className="mr-auto text-xs text-muted-foreground">{busyMessage}</span>
			) : (
				<span id={branchNoteId} className="mr-auto text-pretty text-xs text-muted-foreground">
					{t("chat.edit.branchNote")}
				</span>
			)}
			{onAbandonRecovery ? (
				<Button
					type="button"
					size="sm"
					variant="outline"
					onClick={onAbandonRecovery}
				>
					{t("chat.edit.abandonRecovery")}
				</Button>
			) : null}
			<span className="inline-flex">
				<Button
					type="button"
					size="icon-sm"
					variant="ghost"
					onClick={onCancel}
					disabled={pending || locked}
					aria-label={t("chat.edit.cancel")}
				>
					<X aria-hidden="true" className="size-3.5" />
				</Button>
			</span>
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex">
						<Button
							type="button"
							variant="primary"
							size="icon-sm"
							shape="pill"
							onClick={submit}
							disabled={sendDisabled}
							aria-label={recoveryLabel ?? t("chat.edit.send")}
						>
							{pending ? <Loader2 aria-hidden="true" className="size-3.5 animate-spin" /> : <ArrowUp aria-hidden="true" className="size-3.5" />}
						</Button>
					</span>
				</TooltipTrigger>
				<TooltipContent side="bottom">{busyMessage ?? recoveryLabel ?? t("chat.edit.sendShortcut")}</TooltipContent>
			</Tooltip>
		</div>
	</div>
	);
}
