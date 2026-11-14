import { isMacPlatform } from "../../lib/platform";
import { effectiveShortcutBindings, matchesAppShortcut, shortcutBindingLabel } from "../../../shared/shortcuts";
import { useKeybindingsStore } from "../../stores/keybindings-store";
import { isDialogOrMenuOpen } from "../../lib/dom-selectors";
import { useLatestCallback } from "../../hooks/useStable";
import { useEffect, useLayoutEffect, useRef, useState, type MutableRefObject, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { useChatSelectionPosition } from "../../hooks/useChatSelectionPosition";
import { apiErrorMessage } from "../../lib/api-client";
import { useChatDraftTranslation } from "../../lib/chat-draft-messages";
import {
	selectionRangesEqual,
	selectionToolbarClass,
} from "../../lib/selection-toolbar";
import { cn } from "../../lib/utils";
import { Check, MessageSquarePlus, Trash2 } from "@/components/app-icons";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";

export const ANNOTATION_COMMENT_MAX_LENGTH = 1000;
const DRAFT_HIGHLIGHT = "chat-annotation-draft";
const EDITABLE_TARGET = 'input, textarea, select, [contenteditable], [role="textbox"], .xterm, webview, [data-browser-view]';

type HighlightApi = { Highlight?: new (...ranges: Range[]) => unknown; CSS?: { highlights?: Map<string, unknown> } };

/** Geometry state stays here: scrolling must not render the conversation. */
export function ChatSelectionActions({
	selection,
	viewportRef,
	onInvalid,
	onAdd,
	onSide,
	checkingSide,
	onRemove,
	commentRef,
}: {
	selection: { excerpt: ChatDraftExcerptReference; range: Range };
	viewportRef: RefObject<HTMLDivElement | null>;
	onInvalid: () => void;
	onAdd: (comment: string) => void | Promise<void>;
	onSide?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	checkingSide?: boolean;
	onRemove?: () => void | Promise<void>;
	commentRef?: MutableRefObject<string>;
}) {
	const { t } = useTranslation();
	const overrides = useKeybindingsStore((state) => state.overrides);
	const shortcutHint = effectiveShortcutBindings("add-selection-to-chat", isMacPlatform(), overrides)
		.map((binding) => shortcutBindingLabel(binding, isMacPlatform())).join(" / ");
	const translateDraft = useChatDraftTranslation();
	const editing = Boolean(onRemove);
	const buttonRef = useRef<HTMLDivElement>(null);
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const [comment, setComment] = useState(selection.excerpt.comment ?? "");
	const [pinned, setPinned] = useState(editing);
	const position = useChatSelectionPosition(
		selection.range,
		viewportRef,
		buttonRef,
		onInvalid,
		pinned,
	);
	const locked = useRef(false);
	const alive = useRef(true);
	useEffect(() => {
		alive.current = true;
		return () => {
			alive.current = false;
		};
	}, []);
	useEffect(() => {
		if (commentRef) commentRef.current = comment;
	}, [comment, commentRef]);
	useEffect(() => () => {
		if (commentRef) commentRef.current = "";
	}, [commentRef]);
	useLayoutEffect(() => {
		const api = globalThis as unknown as HighlightApi;
		const registry = api.CSS?.highlights;
		if (!pinned || !registry || !api.Highlight) return;
		const highlight = new api.Highlight(selection.range);
		registry.set(DRAFT_HIGHLIGHT, highlight);
		return () => {
			if (registry.get(DRAFT_HIGHLIGHT) === highlight) registry.delete(DRAFT_HIGHLIGHT);
		};
	}, [pinned, selection.range]);
	useEffect(() => {
		if (!editing || !position?.visible) return;
		textareaRef.current?.focus({ preventScroll: true });
	}, [editing, position?.visible]);
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<string>();
	const run = useLatestCallback(async (action: () => void | Promise<void>, dismiss = false) => {
		if (locked.current) return;
		locked.current = true;
		setPending(true);
		setError(undefined);
		try {
			await action();
			if (!dismiss || !alive.current) return;
			onInvalid();
			const current = window.getSelection();
			const range = current?.rangeCount ? current.getRangeAt(0) : undefined;
			if (selectionRangesEqual(range, selection.range))
				current?.removeAllRanges();
		} catch (failure) {
			if (alive.current) setError(apiErrorMessage(failure));
		} finally {
			locked.current = false;
			if (alive.current) setPending(false);
		}
	});
	const activate = useLatestCallback((side: boolean) => {
		if (side) {
			if (!onSide || checkingSide) return;
			const note = comment.trim();
			return run(() => onSide(note ? { ...selection.excerpt, comment: note } : selection.excerpt), true);
		}
		return run(() => onAdd(comment.trim()));
	});
	useEffect(() => {
		if (!position?.visible) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.defaultPrevented || event.isComposing || isDialogOrMenuOpen()) return;
			const shortcut = matchesAppShortcut("add-selection-to-chat", {
				key: event.key, code: event.code, ctrl: event.ctrlKey,
				meta: event.metaKey, shift: event.shiftKey, alt: event.altKey,
			}, isMacPlatform(), useKeybindingsStore.getState().overrides);
			const typing = event.key.length === 1 && event.key !== " " && !event.metaKey && !event.ctrlKey && !event.altKey;
			if (!shortcut && !typing) return;
			const toolbar = buttonRef.current;
			const origin = event.composedPath()[0];
			const target =
				origin instanceof Element ? origin : document.activeElement;
			if (!toolbar || target?.closest(EDITABLE_TARGET)) return;
			if (
				target &&
				target !== document.body &&
				target !== document.documentElement &&
				!viewportRef.current?.contains(target) &&
				!toolbar.contains(target)
			)
				return;
			const native = window.getSelection();
			const saved = selection.range;
			if (
				!native?.rangeCount ||
				native.isCollapsed ||
				!saved.startContainer.isConnected ||
				!saved.endContainer.isConnected ||
				!viewportRef.current?.contains(saved.startContainer) ||
				!selectionRangesEqual(native.getRangeAt(0), saved)
			)
				return;
			if (typing && !shortcut) {
				textareaRef.current?.focus({ preventScroll: true });
				return;
			}
			event.preventDefault();
			event.stopPropagation();
			if (!event.repeat) void activate(false);
		};
		document.addEventListener("keydown", onKeyDown, true);
		return () => document.removeEventListener("keydown", onKeyDown, true);
	}, [position?.visible, selection, viewportRef, activate]);

	const addLabel = editing ? t("chat.annotation.save") : t("sideChat.addToChat");
	return (
		<TooltipProvider>
			<div
				ref={buttonRef}
				data-chat-selection-actions=""
				className={cn(selectionToolbarClass, "absolute w-80 max-w-[calc(100%-1rem)] items-end gap-1 rounded-xl p-1", !position?.visible && "invisible")}
				style={{ left: position?.left ?? 0, top: position?.top ?? 0 }}
				onMouseDown={(event) => {
					if (event.target !== textareaRef.current) event.preventDefault();
				}}
			>
				<textarea
					ref={textareaRef}
					rows={1}
					value={comment}
					maxLength={ANNOTATION_COMMENT_MAX_LENGTH}
					disabled={pending}
					aria-label={t("chat.annotation.commentLabel")}
					placeholder={t("chat.annotation.commentPlaceholder")}
					className="board-scrollbar max-h-32 min-h-7 min-w-0 flex-1 resize-none self-center border-0 bg-transparent px-2 py-1 font-sans text-[13px] leading-5 text-foreground outline-none [field-sizing:content] placeholder:text-muted-foreground disabled:opacity-60"
					onFocus={() => setPinned(true)}
					onChange={(event) => setComment(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Escape") {
							event.preventDefault();
							event.stopPropagation();
							onInvalid();
						} else if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
							event.preventDefault();
							void activate(false);
						}
					}}
				/>
				{onSide || checkingSide ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								type="button"
								variant="quiet"
								size="icon-xs"
								aria-label={t("sideChat.addToSideChat")}
								disabled={pending || checkingSide}
								onClick={() => void activate(true)}
							>
								<MessageSquarePlus aria-hidden="true" className="size-3.5" />
							</Button>
						</TooltipTrigger>
						<TooltipContent side="top">{t("sideChat.addToSideChat")}</TooltipContent>
					</Tooltip>
				) : null}
				{onRemove ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								type="button"
								variant="destructive-ghost"
								size="icon-xs"
								aria-label={t("chat.annotation.remove")}
								disabled={pending}
								onClick={() => void run(onRemove, true)}
							>
								<Trash2 aria-hidden="true" className="size-3.5" />
							</Button>
						</TooltipTrigger>
						<TooltipContent side="top">{t("chat.annotation.remove")}</TooltipContent>
					</Tooltip>
				) : null}
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							type="button"
							variant="primary"
							size="icon-xs"
							shape="pill"
							aria-label={addLabel}
							disabled={pending}
							onClick={() => void activate(false)}
						>
							<Check aria-hidden="true" className="size-3.5" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="top">{editing ? addLabel : `${addLabel}${shortcutHint ? ` (${shortcutHint})` : ""}`}</TooltipContent>
				</Tooltip>
				{error ? (
					<span
						role="alert"
						className="basis-full px-2 pb-1 text-xs text-destructive break-words"
					>
						{translateDraft(error)}
					</span>
				) : null}
			</div>
		</TooltipProvider>
	);
}
