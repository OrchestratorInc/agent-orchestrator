import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Info, Loader2 } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import type { ConversationContentSummary } from "../../types/conversation";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { CollapsibleHumanBody } from "./CollapsibleHumanBody";
import { ConversationContentItems } from "./ConversationContentItems";
import {
	IMAGE_ATTACHMENT_PATH,
	stagedAttachmentParts,
	withAttachmentReferences,
} from "./messageAttachments";
import { AttachmentStrip, StagedAttachmentItems } from "./StagedAttachmentItems";

export interface HumanMessageEditorProps {
	/** The full prompt text, including AO's attached-files block when the message had one. */
	text: string;
	/** The message as sent. Sending it unchanged would only repeat the same turn. */
	originalText?: string;
	content: ConversationContentSummary[];
	sessionId: string;
	apiBaseUrl: string | null;
	/** The bubble's own classes: the bubble becomes the editor, so nothing moves. */
	className?: string;
	/** Places Cancel and Send in the row the message's hover actions use. */
	actionsClassName?: string;
	/** Drawn first in the row above the box, where the message draws it. */
	leading?: ReactNode;
	/** Drawn after the staged files, above the box, where the message draws them. */
	cloudAttachments?: ReactNode;
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
	originalText,
	content,
	sessionId,
	apiBaseUrl,
	className,
	actionsClassName,
	leading,
	cloudAttachments,
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
	// shown as the bubble shows it and written back on every publish, so the agent
	// still gets it.
	const [draft, setDraft] = useState(() => stagedAttachmentParts(text).body);
	const [attachments, setAttachments] = useState(() => stagedAttachmentParts(text).attachments);
	const published = useRef<string | undefined>(undefined);
	const box = useRef<HTMLDivElement>(null);
	const textarea = useRef<HTMLTextAreaElement>(null);
	// The width the bubble had when editing began. Deleting text never narrows the
	// box below it, so its edge stays put while the person types.
	const [startWidth, setStartWidth] = useState<number>();
	const branchNoteId = useId();
	const composed = withAttachmentReferences(draft.trimEnd(), attachments);
	const original = originalText === undefined ? undefined : stagedAttachmentParts(originalText);
	const unchanged =
		original !== undefined &&
		!recoveryLabel &&
		draft.trimEnd() === original.body.trimEnd() &&
		attachments.join("\n") === original.attachments.join("\n");
	const sendDisabled =
		pending || sendBlocked || busy || unchanged || composed.trim().length === 0 || (locked && !recoveryLabel);
	const busyMessage = busy ? t("chat.edit.stopCurrentTurn") : undefined;
	// Delivery starts by recording the edit for recovery, which hands this editor
	// Retry or Clear while the send is still in flight. Send keeps the label it was
	// clicked with until the send settles, so the label under the spinner, and with
	// it the button's width, stays the same.
	const [sendingRecoveryLabel, setSendingRecoveryLabel] = useState(recoveryLabel);
	if (!pending && sendingRecoveryLabel !== recoveryLabel) setSendingRecoveryLabel(recoveryLabel);
	const shownRecoveryLabel = pending ? sendingRecoveryLabel : recoveryLabel;
	// The bubble's fold is keyed by the text it shows; reading the same key opens the
	// editor folded or open as the bubble was.
	const [foldKey] = useState(() => originalText ?? text);
	const hasImages = attachments.some((path) => IMAGE_ATTACHMENT_PATH.test(path));
	// A native image block is the same picture as its staged image; listing both
	// would show it twice.
	const otherContent = hasImages ? content.filter((item) => item.type !== "image") : content;

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

	useLayoutEffect(() => {
		const width = box.current?.getBoundingClientRect().width;
		if (width) setStartWidth(width);
		// The caret starts after the text, where a person picks up an edit.
		const node = textarea.current;
		node?.setSelectionRange(node.value.length, node.value.length);
	}, []);

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
		<>
			{/* Above the box and outside its fold, where the bubble draws them. */}
			{leading || attachments.length > 0 || cloudAttachments || otherContent.length > 0 ? (
				<AttachmentStrip>
					{leading}
					<StagedAttachmentItems
						paths={attachments}
						sessionId={sessionId}
						apiBaseUrl={apiBaseUrl}
						ariaLabel={t("chat.attachment.list")}
						className="contents"
						onRemove={pending || locked ? undefined : removeAttachment}
					/>
					{cloudAttachments}
					<ConversationContentItems
						content={otherContent}
						ariaLabel={t("chat.edit.preservedContent")}
						imageLabel={t("chat.edit.image")}
						className="contents"
					/>
				</AttachmentStrip>
			) : null}
			<div
				ref={box}
				className={className}
				style={startWidth ? { minWidth: `min(${startWidth}px, 100%)` } : undefined}
			>
				{/* The bubble's own fold, so a long message keeps its height, folded or open.
				    Folded, the editor scrolls instead of clipping, so all of it stays reachable. */}
				<CollapsibleHumanBody stateKey={foldKey} scrollWhenFolded>
					{/* A hidden copy of the draft sizes the field (see styles.css), so the box
					    fits its text exactly as the bubble did and grows only with the text. */}
					<div className="chat-human-edit-field" data-value={draft}>
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
							rows={1}
							cols={1}
							className="w-full min-w-0 resize-none overflow-hidden bg-transparent p-0 break-words whitespace-pre-wrap text-pretty text-foreground outline-none"
						/>
					</div>
				</CollapsibleHumanBody>
				{/* Below the field it belongs to, on its own line, so all of it reads. */}
				{error ? (
					<p role="alert" className="mt-2 text-pretty text-xs text-destructive">
						{error}
					</p>
				) : null}
			</div>
			<div className={cn(actionsClassName, "max-w-full")}>
				{error ? null : busyMessage ? (
					<span className="mr-1 min-w-0 max-w-80 truncate text-xs text-muted-foreground">{busyMessage}</span>
				) : (
					<>
						<span id={branchNoteId} className="sr-only">
							{t("chat.edit.branchNote")}
						</span>
						<Tooltip>
							<TooltipTrigger asChild>
								<span aria-hidden="true" className="flex h-6 items-center text-muted-foreground">
									<Info className="size-3.5" />
								</span>
							</TooltipTrigger>
							<TooltipContent side="bottom">{t("chat.edit.branchNote")}</TooltipContent>
						</Tooltip>
					</>
				)}
				{onAbandonRecovery ? (
					<Button type="button" size="xs" variant="ghost" onClick={onAbandonRecovery}>
						{t("chat.edit.abandonRecovery")}
					</Button>
				) : null}
				<Button
					type="button"
					size="xs"
					variant="ghost"
					onClick={onCancel}
					disabled={pending || locked}
					aria-label={t("chat.edit.cancel")}
				>
					{t("chat.edit.cancelAction")}
				</Button>
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex">
							<Button
								type="button"
								variant="primary"
								size="xs"
								onClick={submit}
								disabled={sendDisabled}
								aria-label={shownRecoveryLabel ?? t("chat.edit.send")}
								className="relative"
							>
								{/* The spinner sits over the label, so the button keeps its width. */}
								<span className={cn(pending && "invisible")}>{shownRecoveryLabel ?? t("chat.edit.sendAction")}</span>
								{pending ? <Loader2 aria-hidden="true" className="absolute size-3.5 animate-spin" /> : null}
							</Button>
						</span>
					</TooltipTrigger>
					<TooltipContent side="bottom">{busyMessage ?? shownRecoveryLabel ?? t("chat.edit.sendShortcut")}</TooltipContent>
				</Tooltip>
			</div>
		</>
	);
}
