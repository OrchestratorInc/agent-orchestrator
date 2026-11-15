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
	text: string;
	originalText?: string;
	content: ConversationContentSummary[];
	sessionId: string;
	apiBaseUrl: string | null;
	className?: string;
	actionsClassName?: string;
	leading?: ReactNode;
	cloudAttachments?: ReactNode;
	pending: boolean;
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
	const [draft, setDraft] = useState(() => stagedAttachmentParts(text).body);
	const [attachments, setAttachments] = useState(() => stagedAttachmentParts(text).attachments);
	const published = useRef<string | undefined>(undefined);
	const box = useRef<HTMLDivElement>(null);
	const textarea = useRef<HTMLTextAreaElement>(null);
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
	const [sendingRecoveryLabel, setSendingRecoveryLabel] = useState(recoveryLabel);
	if (!pending && sendingRecoveryLabel !== recoveryLabel) setSendingRecoveryLabel(recoveryLabel);
	const shownRecoveryLabel = pending ? sendingRecoveryLabel : recoveryLabel;
	const [foldKey] = useState(() => originalText ?? text);
	const hasImages = attachments.some((path) => IMAGE_ATTACHMENT_PATH.test(path));
	const otherContent = hasImages ? content.filter((item) => item.type !== "image") : content;

	useEffect(() => {
		if (text === published.current) return;
		const parts = stagedAttachmentParts(text);
		setDraft(parts.body);
		setAttachments(parts.attachments);
	}, [text]);

	useLayoutEffect(() => {
		const width = box.current?.getBoundingClientRect().width;
		if (width) setStartWidth(width);
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
				<CollapsibleHumanBody stateKey={foldKey} scrollWhenFolded>
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
