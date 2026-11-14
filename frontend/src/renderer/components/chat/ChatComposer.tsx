import { excerptSizeError, fitExcerptContext, LOCAL_CHAT_MESSAGE_BYTES } from "../../../shared/chat-excerpt";
import { upgradeExcerpt } from "../../lib/chat-excerpt-snapshot";
import { hasCodeSelection } from "../diffs/SelectionStableContent";
import { isDialogOrMenuOpen } from "../../lib/dom-selectors";
import { useChatDraftTranslation } from "../../lib/chat-draft-messages";

import {
	cloneElement,
	useCallback,
	useEffect,
	useId,
	useImperativeHandle,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	useSyncExternalStore,
	isValidElement,
	memo,
	type ClipboardEvent,
	type DragEvent,
	type FormEvent,
	type KeyboardEvent,
	type ReactElement,
	type ReactNode,
	type Ref,
} from "react";
import { ArrowUp, CornerUpRight, Loader2, Mic, Paperclip, Play, Plus, Square, X } from "@/components/app-icons";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { isMacPlatform } from "../../lib/platform";
import { useDictation } from "./useDictation";
import { DictationMeter } from "./DictationMeter";
import { OptionMenu, OptionMenuContent, OptionMenuLabel, OptionMenuTrigger } from "../ui/option-menu";
import { ComposerMenuRow } from "./TurnSettingsBar";
import { Button, GHOST_PILL_CLASS } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { formatCapturedFileReferences, type ChatFileReference } from "../../../shared/chat-file-reference";
import { chatDraftAnnotations, writeChatFileReferences } from "../../lib/chat-drafts";
import { ChatAnnotationSummary } from "./ChatAnnotationSummary";
import { UnsentMessageStrip } from "./UnsentMessageStrip";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { apiErrorCode, apiErrorMessage, getApiBaseUrl } from "../../lib/api-client";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";
import {
	ComposerEditor,
	type ComposerEditorHandle,
	type ComposerImage,
	type ComposerEditorSnapshot,
	type ComposerPromptSuggestion,
	type ComposerTrigger,
} from "./ComposerEditor";
import { messageTokenScope, recordSentComposerTokens, sessionTokenScope } from "./sentComposerTokens";
import { moveHighlight, rankFiles, rankSkills, type Suggestion } from "./composerSuggest";
import {
	isSupportedImageAttachment,
	useFileAttachments,
	type FileAttachment,
	MAX_ATTACHMENTS,
	type FileAttachmentPayload,
} from "../../hooks/useFileAttachments";
import { File } from "@/components/app-icons";
import type { ChatSkill, ChatSteerOutcome } from "../../types/conversation";
import {
	acknowledgeChatComposerMutation,
	beginChatComposerMutation,
	cancelChatComposerMutation,
	chatDraftScopeKey,
	chatQueuedAttachmentScopeKey,
	clearAcceptedChatComposer,
	clearRejectedChatComposerDelivery,
	clearUncertainChatComposerDelivery,
	discardUnsentChatComposerDelivery,
	finishChatComposerMutation,
	getChatComposerMutation,
	isChatComposerMutationCurrent,
	loadChatSessionDraft,
	markChatComposerDeliveryAccepted,
	isParkableChatComposerDelivery,
	parkChatComposerDelivery,
	prepareChatComposerDelivery,
	readChatSessionDraft,
	restoreUnsentChatComposerDelivery,
	reviveUnsentChatComposerDelivery,
	subscribeChatDraftRuntime,
	writeChatAttachments,
	writeChatExcerptReferences,
	writeChatComposerText,
	type ChatDraftMutationToken,
	type ChatComposerDelivery,
	type ChatDraftScope,
	type ChatDraftAttachment,
	type ChatDraftExcerptReference,
	type ChatDraftRetainedAttachment,
	type DraftClearResult,
} from "../../lib/chat-drafts";
import { attachmentURL, IMAGE_ATTACHMENT_PATH, isInlineImagePath, proseBesideImages, removeInlineImagePath, withAttachmentReferences } from "./messageAttachments";
import { AttachmentFileText, AttachmentThumbnail } from "./StagedAttachmentItems";
import { rememberAttachmentName } from "../../lib/chat-attachment-names";
import { rememberSentAttachments } from "../../lib/sent-attachment-previews";
import { isCloudAttachmentReference } from "../../lib/cloud-attachment-reference";
import { setChatDraftBoundary } from "../../lib/chat-draft-boundary";
import { subscribeChatComposerReferences, subscribeChatComposerText } from "../../lib/chat-context-bus";

export const AGENT_SWITCH_FAILED = "AGENT_SWITCH_FAILED";
const DEFINITIVE_SEND_REJECTIONS = new Set([
	AGENT_SWITCH_FAILED,
	"TASK_CREATE_FAILED",
	"CHAT_FILE_REFERENCE_INVALID", "CHAT_ANNOTATION_LIMIT", "CHAT_FILE_REFERENCES_SIDE_UNSUPPORTED",
	"validation_error",
	"INVALID_BODY",
	"CHAT_MESSAGE_EMPTY",
	"INVALID_RESOURCE",
	"UNSUPPORTED_ATTACHMENT_TYPE",
	"INVALID_ATTACHMENT_DATA",
	"ATTACHMENT_TOO_LARGE",
	"TOO_MANY_ATTACHMENTS",
	"ATTACHMENTS_TOO_LARGE",
	"SESSION_NOT_FOUND",
	"SESSION_MODE_MISMATCH",
	"CHAT_CONTROLLER_NOT_READY",
	"CHAT_RESUME_FAILED",
	"CHAT_INTERFACE_TRANSITION",
	"COMPOSER_LOCKED",
	"COMPOSER_LEASE_LOST",
	"CHAT_EXCERPT_INVALID",
	"CHAT_EXCERPT_STALE",
	"CHAT_SIDE_UNAVAILABLE",
	"CHAT_SIDE_UNSUPPORTED",
	"CHAT_SIDE_ANCHOR_UNAVAILABLE",
	"CHAT_SIDE_INVALID_REQUEST",
	"CHAT_SIDE_NOT_READY",
	"CHAT_SIDE_RECREATE_REQUIRED",
	"CHAT_SIDE_CLOSED",
	"CHAT_SIDE_IDEMPOTENCY_CONFLICT",
]);

const SEND_CONCEAL_MS = 3000;
const MAX_NATIVE_IMAGE_BYTES = 10 * 1024 * 1024;
const MAX_NATIVE_IMAGES_BYTES = 25 * 1024 * 1024;

function isTextEntryFocused(): boolean {
	const active = document.activeElement;
	if (!(active instanceof HTMLElement)) return false;
	return /^(INPUT|TEXTAREA|SELECT)$/.test(active.tagName) || active.closest('[contenteditable]:not([contenteditable="false"])') !== null;
}

function ComposerBand({ band }: { band?: ReactNode }) {
	const reducedMotion = useReducedMotion();
	return (
		<AnimatePresence initial={false}>
			{band ? (
				<div key="band" className="overflow-hidden">
					<motion.div
						className="chat-composer-band-tray"
						initial={{ y: "100%" }}
						animate={{ y: 0, transition: { duration: reducedMotion ? 0 : 0.15, ease: "easeOut" } }}
						exit={{ y: "100%", transition: { duration: reducedMotion ? 0 : 0.15, ease: "easeIn" } }}
					>
						{band}
					</motion.div>
				</div>
			) : null}
		</AnimatePresence>
	);
}

type HeldSubmit = {
	text: string;
	steer: boolean;
	own: boolean;
	unsentId?: string;
};

const STEER_NEXT_AFTER_SEND_MS = 1000;

const UNSENT_UNDO_MS = 5000;

function fitsUnsentSlot(delivery: ChatComposerDelivery | undefined, scope: ChatDraftScope): delivery is ChatComposerDelivery {
	return isParkableChatComposerDelivery(delivery) && !readChatSessionDraft(scope).unsent;
}

function parksDelivery(delivery: ChatComposerDelivery | undefined, scope: ChatDraftScope | undefined): delivery is ChatComposerDelivery {
	return Boolean(scope && fitsUnsentSlot(delivery, scope) && !getChatComposerMutation(scope).pending);
}

function restoredDeliveryNotice(delivery: ChatComposerDelivery | undefined, parks = false): string | null {
	if (!delivery) return null;
	if (parks) return null;
	if (delivery.state === "accepted") {
		return "chat.draft.acceptedMessage";
	}
	return delivery.kind === "steer"
		? "chat.draft.steerRestart"
		: "chat.draft.sendRestart";
}
export type StoredComposerAttachment = ChatDraftRetainedAttachment & { dataUrl?: string };
export type ChatComposerHandle = {
	focus(): void;
	isEmpty(): boolean;
	setDraft(text: string): void;
};

function useDockEntrance(shown: string | null): boolean {
	const quiet = useRef(shown);
	if (quiet.current !== null && quiet.current !== shown) quiet.current = null;
	return quiet.current === null;
}

export const ChatComposer = memo(function ChatComposer({
	focusRef,
	onSend,
	busy,
	willQueue,
	queuePlaceholder,
	disabled,
	disabledPlaceholder,
	typingLocked = false,
	sendBlockedReason,
	starting,
	holdAttachments,
	onHeldAttachmentsChange,
	attachmentsRefusal,
	settings,
	addMenuItems,
	approval,
	elicitation,
	skills = [],
	filePaths = [],
	filePathsTruncated,
	onFileQuery,
	onStageAttachments,
	cloudAttachments,
	nativeImages,
	onSteer,
	showSteerButton,
	onInterrupt,
	onContinue,
	canSteer,
	sendPending,
	steerPending,
	steerRefusal,
	draftSeed,
	editingQueuedTurnId,
	onCancelQueuedEdit,
	onQueuedDraftChange,
	onDraftEmptyChange,
	onDraftInput,
	queuedDraftScope,
	onQueuedAttachmentsChange,
	onQueuedRetainedAttachmentsChange,
	savingQueuedEditPending,
	queuedEditRecovery,
	commandError,
	notice,
	trayBand,
	queuedDock,
	onCompact,
	compacting,
	compactUnavailable,
	compactBlocked,
	autoFocusKey,
	autoFocus = true,
	draftSessionId,
	draftSessionIncarnation,
	assetBaseUrl,
	remoteHost = false,
	assetSessionId,
	acceptedClientMessageIds,
	emptyPlaceholder,
	promptSuggestion,
	onSelectAnnotation,
	sentMessages,
	sentMessagesScope,
	fileReferencesEnabled = false,
	fileReferenceDelivery,
	messageByteLimit = LOCAL_CHAT_MESSAGE_BYTES,
	parkUnsentSends = true,
}: {
	onSelectAnnotation?: (annotation: { text: string; messageId?: string; revision?: number }) => void;
	sentMessages?: readonly string[];
	sentMessagesScope?: string;
	fileReferencesEnabled?: boolean;
	fileReferenceDelivery?: import("../../../shared/chat-file-reference").FileReferenceDelivery;
	messageByteLimit?: number;
	focusRef?: Ref<ChatComposerHandle>;
	onSend: (
		text: string,
		attachments?: FileAttachmentPayload[],
		clientMessageId?: string,
		retainedContent?: number[],
		excerpts?: ChatDraftExcerptReference[],
	fileReferences?: ChatFileReference[],
		displayText?: string,
	) => void | Promise<unknown>;
	settings?: ReactNode;
	addMenuItems?: ReactNode;
	approval?: ReactNode;
	elicitation?: ReactNode;
	busy?: boolean;
	willQueue?: boolean;
	queuePlaceholder?: string;
	disabled?: boolean;
	disabledPlaceholder?: string;
	typingLocked?: boolean;
	sendBlockedReason?: string;
	starting?: boolean;
	holdAttachments?: boolean;
	onHeldAttachmentsChange?: (attachments: FileAttachmentPayload[], preparing: boolean) => void;
	attachmentsRefusal?: string;
	emptyPlaceholder?: string;
	promptSuggestion?: ComposerPromptSuggestion;
	skills?: ChatSkill[];
	filePaths?: string[];
	onFileQuery?: (query: string) => void;
	filePathsTruncated?: boolean;
	onStageAttachments?: (attachments: FileAttachmentPayload[], signal?: AbortSignal) => Promise<string[]>;
	cloudAttachments?: { maxBytes: number };
	nativeImages?: boolean;
	onSteer?: (text: string, attachments?: FileAttachmentPayload[], clientMessageId?: string, recoverOnly?: boolean) => Promise<ChatSteerOutcome | void>;
	showSteerButton?: boolean;
	onInterrupt?: () => void;
	onContinue?: () => Promise<unknown> | void;
	canSteer?: boolean;
	sendPending?: boolean;
	steerPending?: boolean;
	steerRefusal?: string;
	draftSeed?: { id: string; text: string; attachments?: StoredComposerAttachment[]; stagedAttachments?: ChatDraftAttachment[] };
	editingQueuedTurnId?: string;
	onCancelQueuedEdit?: () => void;
	onQueuedDraftChange?: (text: string) => void;
	onDraftEmptyChange?: (empty: boolean) => void;
	onDraftInput?: (text: string, caret?: number) => void;
	queuedDraftScope?: ChatDraftScope;
	onQueuedAttachmentsChange?: (attachments: ChatDraftAttachment[]) => void;
	onQueuedRetainedAttachmentsChange?: (attachments: ChatDraftRetainedAttachment[]) => void;
	savingQueuedEditPending?: boolean;
	queuedEditRecovery?: boolean;
	commandError?: string;
	notice?: ReactNode;
	trayBand?: ReactNode;
	queuedDock?: ReactNode;
	onCompact?: () => void | Promise<unknown>;
	compacting?: boolean;
	compactUnavailable?: string;
	compactBlocked?: boolean;
	autoFocusKey?: string;
	autoFocus?: boolean;
	draftSessionId?: string;
	draftSessionIncarnation?: string;
	assetBaseUrl?: string;
	remoteHost?: boolean;
	assetSessionId?: string;
	acceptedClientMessageIds?: ReadonlySet<string>;
	parkUnsentSends?: boolean;
}) {
	const translateDraft = useChatDraftTranslation();
	const draftScope = useMemo<ChatDraftScope | undefined>(
		() =>
			draftSessionId
				? {
						sessionId: draftSessionId,
						incarnation: draftSessionIncarnation ?? draftSessionId,
					}
				: undefined,
		[draftSessionId, draftSessionIncarnation],
	);
	const draftScopeKey = draftScope ? chatDraftScopeKey(draftScope) : undefined;
	const attachmentScopeKey = queuedDraftScope
		? chatQueuedAttachmentScopeKey(queuedDraftScope, draftSeed?.id ?? "undefined")
		: draftScopeKey;
	const boundarySessionId = draftSessionId ?? queuedDraftScope?.sessionId;
	const [retainedAttachments, setRetainedAttachments] = useState<StoredComposerAttachment[]>(draftSeed?.attachments ?? []);
	const visibleRetainedAttachments = editingQueuedTurnId ? retainedAttachments : [];
	const [hasText, setHasText] = useState(() =>
		Boolean(draftScope && !queuedDraftScope && readChatSessionDraft(draftScope).composer.text.trim()));
	const hasTextRef = useRef(hasText);
	const [trigger, setTrigger] = useState<ComposerTrigger>();
	const [dismissedKey, setDismissedKey] = useState<string | null>(null);
	const dismissedKeyRef = useRef<string | null>(null);
	const [highlighted, setHighlighted] = useState(0);
	const highlightedRef = useRef(0);
	const [isComposing, setIsComposing] = useState(false);
	const chatSendKeyMode = useUiStore((state) => state.chatSendKeyMode);
	const enterSends = chatSendKeyMode === "enter";
	const [dragging, setDragging] = useState(false);
	const [sendError, setSendError] = useState<string | null>(null);
	const [steerOutcomeNotice, setSteerOutcomeNotice] = useState<string | null>(null);
	const [deliveryRecoveryNotice, setDeliveryRecoveryNotice] = useState<string | null>(null);
	const [deliveryUncertain, setDeliveryUncertain] = useState(
		() =>
			draftScope !== undefined &&
			readChatSessionDraft(draftScope).composer.delivery?.state === "dispatching",
	);
	const [textDraftPersistenceError, setTextDraftPersistenceError] = useState<string | null>(null);
	const [attachmentDraftPersistenceError, setAttachmentDraftPersistenceError] = useState<
		string | null
	>(null);
	const [submitting, setSubmitting] = useState(false);
	const [durableDelivery, setDurableDelivery] = useState<ChatComposerDelivery | undefined>(() =>
		draftScope
			? readChatSessionDraft(draftScope).composer.delivery
			: undefined,
	);
	const [unsent, setUnsent] = useState<ChatComposerDelivery | undefined>(() =>
		draftScope ? readChatSessionDraft(draftScope).unsent : undefined);
	const [retryingUnsent, setRetryingUnsent] = useState<string>();
	const [discardedUnsent, setDiscardedUnsent] = useState<ChatComposerDelivery>();
	const discardedUnsentRef = useRef(discardedUnsent);
	discardedUnsentRef.current = discardedUnsent;
	const [steerNextRequest, setSteerNextRequest] = useState(0);
	const [modifierHeld, setModifierHeld] = useState(false);
	const textRef = useRef("");

	const editor = useRef<ComposerEditorHandle>(null);
	const dictation = useDictation({
		onText: (text) => editor.current?.dictate(text),
		onEnd: (keep) => editor.current?.endDictation(keep),
	});
	const dictating = dictation.phase !== "idle";
	const detachDictation = dictation.detach;
	useEffect(() => {
		if (typingLocked) detachDictation();
	}, [detachDictation, typingLocked]);
	const canDictate = useUiStore((state) => state.developerMode) && isMacPlatform();
	const filePicker = useRef<HTMLInputElement>(null);
	const addMenuTrigger = useRef<HTMLButtonElement>(null);
	const [addMenuBox, setAddMenuBox] = useState<{ lift: number; shift: number; width: number }>();
	const submitInFlight = useRef<Promise<void> | null>(null);
	const [heldSubmit, setHeldSubmit] = useState<HeldSubmit | null>(null);
	const heldDuringSubmit = useRef<HeldSubmit | null>(null);
	const submitAccepted = useRef(false);
	const editorClears = useRef(0);
	const editorClearsAtSubmit = useRef(0);
	const lastSubmitAt = useRef(0);
	const restoreFocusAfterSubmission = useRef(false);
	const menuId = useId();
	const hadQueuedDockRef = useRef(Boolean(queuedDock));
	const previousTrigger = useRef<ComposerTrigger | undefined>(undefined);
	const triggerRef = useRef<ComposerTrigger | undefined>(undefined);
	const automaticDeliveryRecoveryAttempted = useRef<string | undefined>(undefined);
	const historyBrowse = useRef<{ entries: string[]; index: number; draft: string; shown: string } | null>(null);
 useEffect(() => {
  if (!draftSessionId || !draftScope) return;
  return subscribeChatComposerReferences(draftSessionId, (reference) => {
   const draft = readChatSessionDraft(draftScope);
   if (getChatComposerMutation(draftScope).pending || draft.composer.delivery) return false;
   const files = draft.composer.fileReferences ?? [];
   if(files.some(file => file.path===reference.path && file.side===reference.side && file.line===reference.line && file.endLine===reference.endLine && file.scope===reference.scope && file.commitSha===reference.commitSha && JSON.stringify(file.source)===JSON.stringify(reference.source) && JSON.stringify(file.lines)===JSON.stringify(reference.lines)))return true;
   if (files.length + (draft.composer.excerpts?.length ?? 0) >= 8) { setSendError("chat.draft.annotationLimit"); return false; }
   const order = Math.max(Date.now(), ...files.map(item => item.order + 1), ...(draft.composer.excerpts ?? []).map(item => (item.order ?? 0) + 1));
   const result = writeChatFileReferences(draftScope, [...files, { ...reference, order }]);
   composerRevision.current = result.draft.composer.revision;
   setTextDraftPersistenceError(result.ok ? null : "chat.draft.saveFailed");
			if (!result.ok) return false;
			window.requestAnimationFrame(() => window.requestAnimationFrame(() => editor.current?.focus()));
			return true;
		});
	}, [draftSessionId, draftScope]);
	const restoredSeedKey = useRef<string | undefined>(undefined);
	const restoredSessionId = useRef<string | undefined>(undefined);
	const persistedDraft = useMemo(
		() => (draftScope ? readChatSessionDraft(draftScope) : undefined),
		[draftScope],
	);
	const subscribeComposerMutation = useCallback(
		(listener: () => void) => subscribeChatDraftRuntime(draftScope ?? "", listener),
		[draftScope],
	);
	const getComposerMutation = useCallback(
		() => getChatComposerMutation(draftScope ?? ""),
		[draftScope],
	);
	const composerMutation = useSyncExternalStore(
		subscribeComposerMutation,
		getComposerMutation,
		getComposerMutation,
	);
	const optimisticSend = Boolean(durableDelivery?.draft && composerMutation.pending);
	const chatReferences = draftScope
		? readChatSessionDraft(draftScope).composer.excerpts ?? []
		: [];
 const fileReferences = draftScope ? readChatSessionDraft(draftScope).composer.fileReferences ?? [] : [];
 const contextReferences = chatDraftAnnotations({ revision: 0, text: "", attachments: [], excerpts: chatReferences, fileReferences }).map(annotation => annotation.kind === "chat"
  ? annotation.reference
  : { kind: "file" as const, id: annotation.reference.id, text: annotation.reference.path, order: annotation.reference.order, fileReference: annotation.reference });
 const [appliedAcceptanceSequence, setAppliedAcceptanceSequence] = useState(0);
	const composerRevision = useRef(persistedDraft?.composer.revision ?? 0);
	const unsavedText = useRef<{ text: string; revision: number } | undefined>(undefined);
	const synchronouslyClearedDeliveryRevision = useRef<number | undefined>(undefined);
	const restoredAttachments = useMemo<FileAttachment[]>(
		() =>
			((parkUnsentSends && parksDelivery(persistedDraft?.composer.delivery, draftScope) ? undefined : persistedDraft?.composer.delivery?.draft?.attachments) ??
				persistedDraft?.composer.attachments ?? draftSeed?.stagedAttachments)?.map((attachment) => ({
				id: attachment.id,
				name: attachment.name,
				mimeType: attachment.mimeType,
				bytes: attachment.bytes,
				stagedPath: attachment.path,
			})) ?? [],
		[persistedDraft, draftSeed?.stagedAttachments, draftScope, parkUnsentSends],
	);
	const persistAttachments = useCallback(
		(attachments: FileAttachment[]) => {
			const descriptors = attachments.flatMap((attachment) => attachment.stagedPath
				? [{ id: attachment.id, path: attachment.stagedPath, name: attachment.name, mimeType: attachment.mimeType, bytes: attachment.bytes }]
				: []);
			if (!draftScope) {
				onQueuedAttachmentsChange?.(descriptors);
				return;
			}
			const sentAttachments = readChatSessionDraft(draftScope).composer.delivery?.draft?.attachments;
			const result = writeChatAttachments(draftScope, descriptors.filter((attachment) =>
				!sentAttachments?.some((sent) => sent.id === attachment.id)));
			if (result.ok) composerRevision.current = result.draft.composer.revision;
			setAttachmentDraftPersistenceError(
				result.ok
					? null
					: "chat.draft.saveFailed",
			);
		},
		[draftScope, onQueuedAttachmentsChange],
	);
	const prepareAttachments = useCallback(
		async (attachments: FileAttachment[], signal: AbortSignal): Promise<FileAttachment[]> => {
			if (!onStageAttachments) throw new Error("Attachment staging is unavailable");
			const paths = await onStageAttachments(
				attachments.flatMap(({ mimeType, data, name }) =>
					data ? [{ mimeType, data, name }] : [],
				),
				signal,
			);
			if (paths.length !== attachments.length) {
				throw new Error("Attachment staging returned an incomplete result");
			}
			return attachments.map((attachment, index) => {
				const stagedPath = paths[index];
				if (stagedPath) rememberAttachmentName(stagedPath, attachment.name);
				return { ...attachment, stagedPath };
			});
		},
		[onStageAttachments],
	);
	useEffect(() => {
		if (restoredSessionId.current === draftScopeKey) return;
		restoredSessionId.current = draftScopeKey;
		historyBrowse.current = null;
		synchronouslyClearedDeliveryRevision.current = undefined;
		const currentDraft = draftScope ? readChatSessionDraft(draftScope) : undefined;
		composerRevision.current = currentDraft?.composer.revision ?? 0;
		unsavedText.current = undefined;
		setDurableDelivery(currentDraft?.composer.delivery);
		setUnsent(currentDraft?.unsent);
		setRetryingUnsent(undefined);
		setDiscardedUnsent(undefined);
		setAppliedAcceptanceSequence(0);
		setSendError(null);
		setSteerOutcomeNotice(null);
		setTextDraftPersistenceError(null);
		setDeliveryRecoveryNotice(restoredDeliveryNotice(
			currentDraft?.composer.delivery,
			parkUnsentSends && parksDelivery(currentDraft?.composer.delivery, draftScope),
		));
		setDeliveryUncertain(currentDraft?.composer.delivery?.state === "dispatching");
		setAttachmentDraftPersistenceError(null);
		automaticDeliveryRecoveryAttempted.current = undefined;
	}, [draftScope, draftScopeKey, parkUnsentSends]);
	const fileAttachments = useFileAttachments({
		initialAttachments: restoredAttachments,
		initialKey: attachmentScopeKey,
		prepareAttachments: onStageAttachments ? prepareAttachments : undefined,
		onAttachmentsChange: persistAttachments,
		maxAttachmentBytes: cloudAttachments?.maxBytes,
		prepareEach: Boolean(cloudAttachments),
	});
	const { pendingAttachments } = fileAttachments;
	const canAttach = (Boolean(onStageAttachments) || Boolean(holdAttachments)) && !queuedEditRecovery;
	useEffect(() => {
		if (!holdAttachments || !onHeldAttachmentsChange) return;
		onHeldAttachmentsChange(fileAttachments.attachments.flatMap(({ stagedPath, mimeType, data, name }) =>
			!stagedPath && data ? [{ mimeType, data, name }] : []), fileAttachments.preparing);
	}, [fileAttachments.attachments, fileAttachments.preparing, holdAttachments, onHeldAttachmentsChange]);

	const slashCommands = useMemo<ChatSkill[]>(() => {
		if (!onCompact || compactUnavailable === "This agent cannot compact its history") return skills;
		return [
			{
				name: "compact",
				displayName: "compact",
				description: "Summarize earlier history to reclaim context",
				source: "AO",
			},
			...skills.filter((skill) => skill.name !== "compact"),
		];
	}, [compactUnavailable, onCompact, skills]);

	const suggestionsFor = useCallback((currentTrigger?: ComposerTrigger): Suggestion[] => {
		if (!currentTrigger || currentTrigger.key === dismissedKeyRef.current) return [];
		if (currentTrigger.kind === "skill") {
			return rankSkills(slashCommands, currentTrigger.query);
		}
		return rankFiles(filePaths, currentTrigger.query);
	}, [slashCommands, filePaths]);

	const suggestions: Suggestion[] = useMemo(
		() => suggestionsFor(trigger),
		[trigger, dismissedKey, suggestionsFor],
	);
	const fileQuery = trigger?.kind === "file" ? trigger.query : undefined;
	useEffect(() => {
		if (fileQuery !== undefined) onFileQuery?.(fileQuery);
	}, [fileQuery, onFileQuery]);

	const menuOpen = suggestions.length > 0;
	const activeIndex = Math.min(highlighted, suggestions.length - 1);

	const staged = fileAttachments.attachments.length > 0 || visibleRetainedAttachments.length > 0;
	const steerInFlight = Boolean(
		submitting &&
			!deliveryUncertain &&
			durableDelivery?.kind === "steer" &&
			durableDelivery.state === "dispatching",
	);
	const sendInFlight = steerInFlight || Boolean(
		submitting &&
			!staged &&
			!deliveryUncertain &&
			durableDelivery?.kind === "send" &&
			durableDelivery.state === "dispatching",
	);
	const [concealExpired, setConcealExpired] = useState(false);
	useEffect(() => {
		if (!sendInFlight) {
			setConcealExpired(false);
			return;
		}
		const timer = window.setTimeout(() => setConcealExpired(true), SEND_CONCEAL_MS);
		return () => window.clearTimeout(timer);
	}, [sendInFlight]);
	const sendConcealed = sendInFlight && !concealExpired;
	const nextDraftHasChanged = Boolean(durableDelivery?.draft && composerRevision.current > durableDelivery.revision + 1);
	const stripConcealed = steerInFlight && sendConcealed;
	const stripAttachments = (optimisticSend || nextDraftHasChanged || stripConcealed ? [] : [...visibleRetainedAttachments, ...fileAttachments.attachments]).map((file) => {
		const path = "stagedPath" in file ? file.stagedPath : "path" in file ? file.path : undefined;
		const assetOrigin = remoteHost ? assetBaseUrl : assetBaseUrl ?? getApiBaseUrl();
		const preview = file.dataUrl ?? (path && !isCloudAttachmentReference(path) && assetOrigin !== undefined && IMAGE_ATTACHMENT_PATH.test(path)
			? attachmentURL(assetOrigin, assetSessionId ?? boundarySessionId ?? "", path) : undefined);
		return { file, path, preview };
	});
	const composerImages: ComposerImage[] = stripAttachments.flatMap(({ file, path, preview }) =>
		path && IMAGE_ATTACHMENT_PATH.test(path) ? [{ path, name: file.name, src: preview }] : []);
	const controlsDisabled = Boolean(disabled || submitting);
	const hasDraft = hasText || staged || contextReferences.length > 0;
	useEffect(() => {
		onDraftEmptyChange?.(!hasDraft);
	}, [hasDraft, onDraftEmptyChange]);
	const savingQueuedEdit = Boolean(editingQueuedTurnId);
	const acceptedMutationWaiting = Boolean(
		composerMutation.accepted &&
			composerMutation.accepted.sequence > appliedAcceptanceSequence,
	);
	const acceptedClearFailed = composerMutation.accepted?.result.ok === false;
	const draftMutationPending =
		submitting || composerMutation.pending || acceptedMutationWaiting || Boolean(durableDelivery);
	const canRecoverDelivery = Boolean(
		durableDelivery &&
			(!busy || durableDelivery.state === "accepted") &&
			!disabled &&
			!typingLocked &&
			!sendBlockedReason &&
			!steerPending &&
		!savingQueuedEditPending &&
			!submitting &&
			!composerMutation.pending,
	);
	const canAbandonUncertainSteer = Boolean(
		deliveryUncertain &&
			durableDelivery?.kind === "steer" &&
			durableDelivery.state === "dispatching" &&
			draftScope &&
			!submitting,
	);
	const canSend =
		hasDraft &&
		(savingQueuedEdit || !busy) &&
		!disabled &&
		!typingLocked &&
		!sendBlockedReason &&
		!steerPending &&
		!savingQueuedEditPending &&
		!draftMutationPending &&
			!acceptedClearFailed &&
			!durableDelivery &&
			!fileAttachments.preparing;
	const sendActionEnabled = canSend || canRecoverDelivery;
	const queuesDraft = Boolean(willQueue && !savingQueuedEdit && !queuedEditRecovery);
	const canSteerDraft =
		Boolean(canSteer && onSteer) && !savingQueuedEdit && contextReferences.length === 0;
	const steersDraft = enterSends && modifierHeld && canSteerDraft;
	const showsSteer = steersDraft && !durableDelivery && !queuedEditRecovery;
	const sendActionLabel = translateDraft(durableDelivery && !submitting
		? durableDelivery.state === "accepted"
			? "chat.draft.clearMessage"
			: "chat.draft.retryMessage"
		: queuedEditRecovery
			? "chat.draft.retryEdit"
			: showsSteer ? "Steer message" : queuesDraft ? "Queue message" : "Send message");
	const canStopTurn = Boolean(
		willQueue && onInterrupt && !controlsDisabled && !hasDraft && !savingQueuedEdit,
	);
	const canContinueTurn = Boolean(
		onContinue && !canStopTurn && !controlsDisabled && !hasDraft && !savingQueuedEdit && !durableDelivery,
	);
	const [continuing, setContinuing] = useState(false);
	const continueTurn = () => {
		if (!onContinue || continuing) return;
		setSendError(null);
		setContinuing(true);
		void Promise.resolve().then(onContinue)
			.catch((error: unknown) => setSendError(apiErrorMessage(error, "Couldn't continue the turn. Try again.")))
			.finally(() => setContinuing(false));
	};
	const canSteerNext =
		Boolean(canSteer && onSteer) &&
		!controlsDisabled &&
		!hasDraft &&
		!savingQueuedEdit &&
		Boolean(queuedDock);
	const sendKeyHint = enterSends ? "Enter" : "Cmd/Ctrl+Enter";
	const newlineHint = enterSends ? "Shift+Enter for newline" : "Enter for newline";
	const sendHint = showsSteer && !menuOpen
		? "Steer into running turn"
		: `${sendKeyHint} to ${menuOpen ? "insert" : savingQueuedEdit ? "save edit" : willQueue ? "queue" : "send"}; ${newlineHint}`;
	const persistedText = persistedDraft?.composer.text;
	const draftSeedId = draftSeed?.id ?? (draftScopeKey ? `session:${draftScopeKey}` : undefined);
	const draftSeedText = draftSeed?.text ?? persistedText;
	const draftPersistenceError =
		textDraftPersistenceError ?? attachmentDraftPersistenceError;

	useEffect(() => {
		if (!boundarySessionId) return;
		const deliveryWasSynchronouslyCleared = Boolean(
			durableDelivery &&
				synchronouslyClearedDeliveryRevision.current === durableDelivery.revision,
		);
		setChatDraftBoundary(
			boundarySessionId,
			"composer",
			[
				...(draftPersistenceError && !deliveryWasSynchronouslyCleared
					? (["persistence-failed"] as const)
					: []),

				...(fileAttachments.preparing && !cloudAttachments ? (["pending-attachments"] as const) : []),
			],
		);
	}, [cloudAttachments, draftPersistenceError, boundarySessionId, durableDelivery, fileAttachments.preparing]);

	useEffect(
		() => () => {
			if (boundarySessionId) setChatDraftBoundary(boundarySessionId, "composer", undefined);
		},
		[boundarySessionId],
	);
	const queuedDockWithSteer = isValidElement<{
		canSteerNext?: boolean;
		steerNextRequest?: number;
		sending?: boolean;
	}>(queuedDock)
		? cloneElement(queuedDock, { canSteerNext, steerNextRequest, sending: submitting })
		: queuedDock;

	const focusEditor = useCallback(() => {
		if (!autoFocus || disabled) return;
		editor.current?.focus();
	}, [autoFocus, disabled]);

	const previousAutoFocusKey = useRef(autoFocusKey);
	useEffect(() => {
		const changedSession = previousAutoFocusKey.current !== autoFocusKey;
		previousAutoFocusKey.current = autoFocusKey;
		if (!changedSession && (window.getSelection()?.isCollapsed === false || hasCodeSelection(document.body))) return;
		focusEditor();
	}, [autoFocusKey, focusEditor]);

	const hasQueuedDock = Boolean(queuedDock);
	const restoreFocusAfterQueueAppears =
		hasQueuedDock &&
		!hadQueuedDockRef.current &&
		typeof document !== "undefined" &&
		document.activeElement?.getAttribute("aria-label") === "Message the agent";
	useLayoutEffect(() => {
		hadQueuedDockRef.current = hasQueuedDock;
		if (restoreFocusAfterQueueAppears) editor.current?.focus();
	}, [hasQueuedDock, restoreFocusAfterQueueAppears]);

	useEffect(() => {
		if (!autoFocus) return;

		const onWindowFocus = () => {
			if (isDialogOrMenuOpen() || window.getSelection()?.isCollapsed === false || hasCodeSelection(document.body)) return;
			if (isTextEntryFocused()) return;
			focusEditor();
		};
		const onVisibilityChange = () => {
			if (document.visibilityState === "visible") onWindowFocus();
		};

		window.addEventListener("focus", onWindowFocus);
		document.addEventListener("visibilitychange", onVisibilityChange);
		return () => {
			window.removeEventListener("focus", onWindowFocus);
			document.removeEventListener("visibilitychange", onVisibilityChange);
		};
	}, [autoFocus, focusEditor]);

	const clearEditorView = useCallback(() => {
		editorClears.current += 1;
		historyBrowse.current = null;
		textRef.current = "";
		hasTextRef.current = false;
		setHasText(false);
		setTrigger(undefined);
		triggerRef.current = undefined;
		previousTrigger.current = undefined;
		dismissedKeyRef.current = null;
		setDismissedKey(null);
		highlightedRef.current = 0;
		setHighlighted(0);
		editor.current?.clear();
	}, []);

	const applyAcceptedDraftResult = useCallback(
		(result: DraftClearResult) => {
			setDurableDelivery(result.draft.composer.delivery);
			if (!result.ok) {
				setDeliveryRecoveryNotice(
					"chat.draft.acceptedMessage",
				);
				setTextDraftPersistenceError(
					"chat.draft.clearMessageFailed",
				);
				return false;
			}
			if (!unsavedText.current) composerRevision.current = result.draft.composer.revision;
			setTextDraftPersistenceError(unsavedText.current ? "chat.draft.saveFailed" : null);
			setDeliveryRecoveryNotice(null);
			if (!unsavedText.current && !result.cleared) {
				textRef.current = result.draft.composer.text;
				hasTextRef.current = textRef.current.trim().length > 0;
				setHasText(hasTextRef.current);
				if (editor.current?.getSnapshot().text !== textRef.current) {
					historyBrowse.current = null;
					editor.current?.setText(textRef.current);
				}
			} else if (!unsavedText.current && editor.current?.getSnapshot().text !== "") {
				clearEditorView();
			}
			if (result.draft.composer.attachments.length === 0) {
				fileAttachments.clear();
			} else {
				for (const attachment of fileAttachments.getAttachments()) {
					if (!result.draft.composer.attachments.some((kept) => kept.id === attachment.id)) {
						fileAttachments.remove(attachment.id);
					}
				}
			}
			return result.cleared && !unsavedText.current;
		},
		[clearEditorView, fileAttachments],
	);

	const clearAcceptedDraft = useCallback(
		(acceptedRevision: number, mutationToken?: ChatDraftMutationToken) => {
			if (!draftScope) {
				clearEditorView();
				fileAttachments.clear();
				return true;
			}
			const saved = unsavedText.current
				? writeChatComposerText(draftScope, unsavedText.current.text, undefined, unsavedText.current.revision)
				: undefined;
			if (saved?.ok && saved.draft.composer.text === unsavedText.current?.text) unsavedText.current = undefined;
			const result: DraftClearResult = saved?.ok === false
				? { ok: false, cleared: false, draft: saved.draft }
				: clearAcceptedChatComposer(draftScope, acceptedRevision, undefined,
					fileAttachments.getAttachments().map((attachment) => attachment.id));
			if (result.ok && result.cleared && draftSessionId) {
				synchronouslyClearedDeliveryRevision.current = acceptedRevision;
				setChatDraftBoundary(draftSessionId, "composer", undefined);
			}
			if (mutationToken) {
				finishChatComposerMutation(
					draftScope,
					mutationToken,
					acceptedRevision,
					result,
				);
				return result.ok && result.cleared;
			}
			return applyAcceptedDraftResult(result);
		},
		[
			applyAcceptedDraftResult,
			clearEditorView,
			draftScope,
			draftSessionId,
			fileAttachments,
		],
	);

	const acceptAndClearDurableDelivery = useCallback(
		(delivery: ChatComposerDelivery, mutationToken?: ChatDraftMutationToken) => {
			if (!draftScope) return clearAcceptedDraft(delivery.revision, mutationToken);
			const accepted = markChatComposerDeliveryAccepted(
				draftScope,
				delivery.clientMessageId,
				delivery.revision,
			);
			if (!accepted.ok) {
				if (mutationToken) cancelChatComposerMutation(draftScope, mutationToken);
				setDurableDelivery(delivery);
				setTextDraftPersistenceError(
					"chat.draft.recordMessageFailed",
				);
				return false;
			}
			return clearAcceptedDraft(delivery.revision, mutationToken);
		},
		[clearAcceptedDraft, draftScope],
	);

	const abandonUncertainSteer = useCallback(() => {
		if (!draftScope || !durableDelivery || durableDelivery.kind !== "steer") return;
		const result = clearUncertainChatComposerDelivery(
			draftScope,
			durableDelivery.clientMessageId,
			durableDelivery.revision,
		);
		setDurableDelivery(result.draft.composer.delivery);
		if (!result.ok) {
			setTextDraftPersistenceError(
				"chat.draft.abandonFailed",
			);
			return;
		}
		setDeliveryUncertain(false);
		setTextDraftPersistenceError(null);
		setDeliveryRecoveryNotice(null);
		setSteerOutcomeNotice(
			"chat.draft.abandonedSteer",
		);
	}, [draftScope, durableDelivery]);

	useEffect(() => {
		const accepted = composerMutation.accepted;
		if (!accepted || accepted.sequence <= appliedAcceptanceSequence) return;
		applyAcceptedDraftResult(accepted.result);
		setAppliedAcceptanceSequence(accepted.sequence);
		if (draftScope) acknowledgeChatComposerMutation(draftScope, accepted.sequence);
	}, [
		appliedAcceptanceSequence,
		applyAcceptedDraftResult,
		composerMutation.accepted,
		draftScope,
	]);

	useEffect(() => {
		if (unsavedText.current || (composerMutation.pending && restoredSeedKey.current !== undefined)) return;
		const loadedDraft = draftScope ? loadChatSessionDraft(draftScope) : undefined;
		if (loadedDraft?.ok === false) return;
		const committedDraft = loadedDraft?.draft;
		const parking = parkUnsentSends && !discardedUnsentRef.current && parksDelivery(committedDraft?.composer.delivery, draftScope);
		const sentDraft = parking ? undefined : committedDraft?.composer.delivery?.draft;
		if (committedDraft && (!sentDraft || restoredSeedKey.current === undefined)) {
			fileAttachments.reconcilePersistedAttachments(
				(sentDraft?.attachments ?? committedDraft.composer.attachments).map((attachment) => ({
					id: attachment.id,
					name: attachment.name,
					mimeType: attachment.mimeType,
					bytes: attachment.bytes,
					stagedPath: attachment.path,
				})),
			);
		}
		const committedSeedText =
			draftSeed?.text ??
			(committedDraft
				? committedDraft.composer.text || (!getChatComposerMutation(draftScope ?? "").pending
					? sentDraft?.text ?? ""
					: "")
				: draftSeedText);
		if (committedSeedText === undefined) {
			restoredSeedKey.current = undefined;
			return;
		}
		const seedKey = editingQueuedTurnId ? draftSeedId : JSON.stringify([draftSeedId, committedSeedText]);
		if (restoredSeedKey.current === seedKey) return;
		restoredSeedKey.current = seedKey;
		textRef.current = committedSeedText;
		hasTextRef.current = committedSeedText.trim().length > 0;
		setHasText(hasTextRef.current);
		if (editor.current?.getSnapshot().text !== committedSeedText) {
			historyBrowse.current = null;
			editor.current?.setText(committedSeedText);
			dismissedKeyRef.current = null;
			setDismissedKey(null);
			highlightedRef.current = 0;
			setHighlighted(0);
		}
		if (draftSeed) {
			setSendError(null);
			setSteerOutcomeNotice(null);
		}
		if (draftScope && draftSeed) {
			const result = writeChatComposerText(draftScope, committedSeedText);
			if (result.ok) composerRevision.current = result.draft.composer.revision;
			setTextDraftPersistenceError(
				result.ok
					? null
					: "chat.draft.saveFailed",
			);
		}
	}, [
		composerMutation.pending,
		draftScope,
		draftSeed,
		draftSeedId,
		draftSeedText,
		editingQueuedTurnId,
		fileAttachments.reconcilePersistedAttachments,
		parkUnsentSends,
	]);

	useEffect(() => {
		if (!durableDelivery || !draftScope) return;
		if (composerMutation.pending || composerMutation.accepted) return;
		const current = loadChatSessionDraft(draftScope);
		const delivery = current.ok ? current.draft.composer.delivery : durableDelivery;
		if (!delivery || delivery.clientMessageId !== durableDelivery.clientMessageId) {
			setDurableDelivery(delivery);
			return;
		}
		const observed = acceptedClientMessageIds?.has(delivery.clientMessageId);
		if (delivery.state !== "accepted" && !observed) return;
		if (automaticDeliveryRecoveryAttempted.current === delivery.clientMessageId) return;
		automaticDeliveryRecoveryAttempted.current = delivery.clientMessageId;
		acceptAndClearDurableDelivery(delivery);
	}, [
		acceptAndClearDurableDelivery,
		acceptedClientMessageIds,
		composerMutation.pending,
		composerMutation.accepted,
		draftScope,
		durableDelivery,
	]);

	useEffect(() => {
		if (!parkUnsentSends || !draftScope || retryingUnsent || !parksDelivery(durableDelivery, draftScope)) return;
		if (submitting || composerMutation.accepted) return;
		if (unsent || discardedUnsent) return;
		if (textDraftPersistenceError === "chat.draft.recordMessageFailed") return;
		if (acceptedClientMessageIds?.has(durableDelivery.clientMessageId)) return;
		const result = parkChatComposerDelivery(draftScope, durableDelivery.clientMessageId);
		if (!result.ok) return;
		setDurableDelivery(undefined);
		setUnsent(result.draft.unsent);
		setDeliveryUncertain(false);
		setDeliveryRecoveryNotice(null);
		if (unsavedText.current) return;
		composerRevision.current = result.draft.composer.revision;
		if (textRef.current !== result.draft.composer.text) {
			historyBrowse.current = null;
			textRef.current = result.draft.composer.text;
			hasTextRef.current = textRef.current.trim().length > 0;
			setHasText(hasTextRef.current);
			editor.current?.setText(textRef.current);
		}
		fileAttachments.reconcilePersistedAttachments(result.draft.composer.attachments.map((attachment) => ({
			id: attachment.id,
			name: attachment.name,
			mimeType: attachment.mimeType,
			bytes: attachment.bytes,
			stagedPath: attachment.path,
		})));
	}, [
		acceptedClientMessageIds,
		composerMutation.accepted,
		composerMutation.pending,
		discardedUnsent,
		draftScope,
		durableDelivery,
		fileAttachments.reconcilePersistedAttachments,
		parkUnsentSends,
		retryingUnsent,
		submitting,
		textDraftPersistenceError,
		unsent,
	]);

	useEffect(() => {
		if (!draftScope || !unsent || !acceptedClientMessageIds?.has(unsent.clientMessageId)) return;
		if (discardUnsentChatComposerDelivery(draftScope, unsent.clientMessageId).ok) setUnsent(undefined);
	}, [acceptedClientMessageIds, draftScope, unsent]);

	const retryUnsent = () => {
		if (!draftScope || !unsent || durableDelivery) return;
		const result = reviveUnsentChatComposerDelivery(draftScope, unsent.clientMessageId);
		if (!result.ok) {
			setTextDraftPersistenceError("chat.draft.unsentSaveFailed");
			return;
		}
		setSendError(null);
		setTextDraftPersistenceError(null);
		setUnsent(undefined);
		setRetryingUnsent(unsent.clientMessageId);
		setDurableDelivery(result.draft.composer.delivery);
	};
	const submitRef = useRef<() => Promise<void>>(() => Promise.resolve());
	const retryStarted = useRef<string | undefined>(undefined);
	useEffect(() => {
		if (!retryingUnsent || durableDelivery?.clientMessageId !== retryingUnsent || retryStarted.current === retryingUnsent) return;
		retryStarted.current = retryingUnsent;
		const finish = () => {
			retryStarted.current = undefined;
			setRetryingUnsent(undefined);
		};
		void submitRef.current().then(finish, finish);
	}, [durableDelivery, retryingUnsent]);

	const discardUnsent = () => {
		if (!draftScope || !unsent) return;
		if (!discardUnsentChatComposerDelivery(draftScope, unsent.clientMessageId).ok) {
			setTextDraftPersistenceError("chat.draft.unsentSaveFailed");
			return;
		}
		setUnsent(undefined);
		setDiscardedUnsent(unsent);
	};
	const undoDiscardUnsent = () => {
		if (!draftScope || !discardedUnsent) return;
		const result = restoreUnsentChatComposerDelivery(draftScope, discardedUnsent);
		if (result.ok) setUnsent(result.draft.unsent);
		setDiscardedUnsent(undefined);
	};
	useEffect(() => {
		if (!discardedUnsent) return;
		const timer = window.setTimeout(() => setDiscardedUnsent(undefined), UNSENT_UNDO_MS);
		return () => window.clearTimeout(timer);
	}, [discardedUnsent]);

	const approvalActive = Boolean(approval);
	const previousApprovalActive = useRef(approvalActive);
	useEffect(() => {
		const wasActive = previousApprovalActive.current;
		previousApprovalActive.current = approvalActive;
		if (!wasActive || approvalActive) return;
		editor.current?.setText(textRef.current);
	}, [approvalActive]);

	const previousEditingQueuedTurnIdRef = useRef(editingQueuedTurnId);
	useEffect(() => {
		const previous = previousEditingQueuedTurnIdRef.current;
		previousEditingQueuedTurnIdRef.current = editingQueuedTurnId;
		if (previous && !editingQueuedTurnId) {
			clearEditorView();
		}
	}, [clearEditorView, editingQueuedTurnId]);

	const persistComposerText = useCallback((text: string) => {
		if (!draftScope) return;
		const result = writeChatComposerText(draftScope, text);
		if (result.ok) composerRevision.current = result.draft.composer.revision;
		unsavedText.current = result.ok ? undefined : { text, revision: composerRevision.current };
		if (!result.ok || !result.draft.composer.delivery) {
			setTextDraftPersistenceError(
				result.ok
					? null
					: "chat.draft.saveFailed",
			);
		}
	}, [draftScope]);
	useEffect(() => {
		if (!draftSessionId || !draftScope) return;
		return subscribeChatComposerText(draftSessionId, (text) => {
			if (getChatComposerMutation(draftScope).pending || readChatSessionDraft(draftScope).composer.delivery) return false;
			const current = textRef.current.replace(/\s+$/, "");
			const next = current ? `${current}\n\n${text}\n\n` : `${text}\n\n`;
			textRef.current = next;
			editor.current?.setText(next, "end");
			persistComposerText(next);
			window.requestAnimationFrame(() => window.requestAnimationFrame(() => editor.current?.focus()));
			return true;
		});
	}, [draftSessionId, draftScope, persistComposerText]);

	const draftCaretRef = useRef<number | undefined>(undefined);
	const onEditorChange = useCallback((snapshot: ComposerEditorSnapshot) => {
		if (optimisticSend && !snapshot.text && !textRef.current) return;
		if (historyBrowse.current && snapshot.text !== historyBrowse.current.shown) historyBrowse.current = null;
		const previousText = textRef.current;
		textRef.current = snapshot.text;
		onQueuedDraftChange?.(snapshot.text);
		if (snapshot.text !== previousText && !historyBrowse.current) persistComposerText(snapshot.text);
		if (snapshot.text !== previousText || snapshot.caret !== draftCaretRef.current) {
			draftCaretRef.current = snapshot.caret;
			onDraftInput?.(snapshot.text, snapshot.caret);
		}
		if (hasTextRef.current !== snapshot.hasText) {
			hasTextRef.current = snapshot.hasText;
			setHasText(snapshot.hasText);
		}

		const previous = previousTrigger.current;
		const next = snapshot.trigger;
		triggerRef.current = next;
		if (
			previous?.key !== next?.key ||
			previous?.end !== next?.end ||
			previous?.query !== next?.query ||
			previous?.kind !== next?.kind
		) {
			highlightedRef.current = 0;
			setHighlighted(0);
			previousTrigger.current = next;
			setTrigger(next);
		}
		if (dismissedKeyRef.current && next?.key !== dismissedKeyRef.current) {
			dismissedKeyRef.current = null;
			setDismissedKey(null);
		}
	}, [onDraftInput, onQueuedDraftChange, optimisticSend, persistComposerText]);
	const walkedScope = useRef(sentMessagesScope);
	useEffect(() => {
		if (walkedScope.current === sentMessagesScope) return;
		walkedScope.current = sentMessagesScope;
		const browse = historyBrowse.current;
		if (!browse) return;
		historyBrowse.current = null;
		editor.current?.setText(browse.draft, "start");
		const snapshot = editor.current?.getSnapshot();
		if (snapshot) onEditorChange(snapshot);
	}, [onEditorChange, sentMessagesScope]);
	useImperativeHandle(
		focusRef,
		() => ({
			focus: focusEditor,
			isEmpty: () => !hasDraft,
			setDraft: (text: string) => {
				editor.current?.setText(text);
				const snapshot = editor.current?.getSnapshot();
				if (snapshot) onEditorChange(snapshot);
				editor.current?.focus();
			},
		}),
		[focusEditor, hasDraft, onEditorChange],
	);

	const pick = useCallback((value: string) => {
		const currentTrigger = triggerRef.current;
		if (!currentTrigger) return;
		editor.current?.insertToken(currentTrigger, value);
		triggerRef.current = undefined;
		previousTrigger.current = undefined;
		setTrigger(undefined);
		highlightedRef.current = 0;
		setHighlighted(0);
		dismissedKeyRef.current = null;
		setDismissedKey(null);
	}, []);

	useEffect(() => {
		if (isComposing || !trigger || trigger.kind !== "skill" || trigger.key === dismissedKey) return;
		if (historyBrowse.current) return;
		const query = trigger.query.toLowerCase();
		if (!query) return;
		const exact = slashCommands.find((skill) => skill.name.toLowerCase() === query);
		if (!exact) return;

		const hasLongerPrefix = slashCommands.some((skill) => {
			const name = skill.name.toLowerCase();
			return name.length > query.length && name.startsWith(query);
		});
		if (!hasLongerPrefix) pick(exact.name);
	}, [dismissedKey, isComposing, pick, slashCommands, trigger]);

	useLayoutEffect(() => {
		if (submitting || !restoreFocusAfterSubmission.current) return;
		restoreFocusAfterSubmission.current = false;
		if (typeof document === "undefined") return;
		const active = document.activeElement;
		if (active === document.body || active === null) editor.current?.focus();
	}, [submitting]);

	const completeFromEditor = useCallback(
		(snapshot: ComposerEditorSnapshot, key: "Enter" | "Tab"): string | undefined => {
			const currentTrigger = snapshot.trigger;
			if (!currentTrigger) return undefined;
			if (key === "Enter" && snapshot.text.trim() === "/compact" && onCompact) {
				return undefined;
			}
			const matches = suggestionsFor(currentTrigger);
			const chosen = matches[Math.min(highlightedRef.current, matches.length - 1)];
			if (!chosen) return undefined;
			triggerRef.current = currentTrigger;
			highlightedRef.current = 0;
			dismissedKeyRef.current = null;
			return chosen.value;
		},
		[onCompact, skills, suggestionsFor],
	);

	function submit(event?: FormEvent, forceSteer?: boolean): Promise<void> {
		event?.preventDefault();
		if (sendBlockedReason) return Promise.resolve();
		if (submitInFlight.current) {
			if (
				!savingQueuedEdit &&
				editorClears.current !== editorClearsAtSubmit.current &&
				textRef.current.trim()
			) {
				heldDuringSubmit.current = { text: textRef.current, steer: Boolean(forceSteer), own: false, unsentId: parkedUnsentId() };
			}
			return submitInFlight.current;
		}
		if (typingLocked) return Promise.resolve();
		setHeldSubmit(null);
		heldDuringSubmit.current = null;
		submitAccepted.current = false;
		editorClearsAtSubmit.current = editorClears.current;
		lastSubmitAt.current = Date.now();
		dictation.detach();
		historyBrowse.current = null;
		restoreFocusAfterSubmission.current =
			typeof document !== "undefined" &&
			document.activeElement?.getAttribute("aria-label") === "Message the agent";
		setSubmitting(true);
		const pending = performSubmit(forceSteer);
		submitInFlight.current = pending;
		const release = () => {
			if (submitInFlight.current !== pending) return;
			submitInFlight.current = null;
			lastSubmitAt.current = Date.now();
			const held = heldDuringSubmit.current;
			heldDuringSubmit.current = null;
			if (held && (held.own || submitAccepted.current)) setHeldSubmit(held);
			setSubmitting(false);
		};
		void pending.then(release, release);
		return pending;
	}

	function holdOwnDraft(forceSteer?: boolean) {
		if (savingQueuedEdit || !textRef.current.trim()) return false;
		heldDuringSubmit.current = { text: textRef.current, steer: Boolean(forceSteer), own: true, unsentId: parkedUnsentId() };
		return true;
	}

	function parkedUnsentId() {
		return draftScope ? readChatSessionDraft(draftScope).unsent?.clientMessageId : undefined;
	}

	async function performSubmit(forceSteer?: boolean) {
		const mutationToken = draftScope && !savingQueuedEdit
			? beginChatComposerMutation(draftScope)
			: undefined;
		if (draftScope && !savingQueuedEdit && !mutationToken) {
			if (!getChatComposerMutation(draftScope).pending) holdOwnDraft(forceSteer);
			return;
		}
		try {
			await performClaimedSubmit(forceSteer, mutationToken);
		} finally {
			if (draftScope && mutationToken) cancelChatComposerMutation(draftScope, mutationToken);
		}
	}

	async function performClaimedSubmit(forceSteer?: boolean, mutationToken?: ChatDraftMutationToken) {
		let currentText = textRef.current;
		let body = currentText.trim();
		const savedDraft = durableDelivery && draftScope ? loadChatSessionDraft(draftScope) : undefined;
		const recoveringDelivery = savedDraft?.ok &&
			savedDraft.draft.composer.delivery?.clientMessageId !== durableDelivery?.clientMessageId
			? undefined
			: durableDelivery;
		const sendNativeImages = recoveringDelivery?.nativeImages ?? Boolean(nativeImages);
		setSendError(null);
		setSteerOutcomeNotice(null);

		if (!recoveringDelivery && !savingQueuedEdit && body === "/compact" && onCompact) {
			if (fileReferences.length) { setSendError("chat.draft.fileReferencesCompactUnsupported"); return; }
			if (draftScope && !mutationToken) return;
			let mutationFinished = false;
			setSubmitting(true);
			try {
				if (compactBlocked) {
					setSendError("Stop the current turn before compacting.");
					return;
				}
				if (compacting) {
					setSendError("Conversation history is already being compacted.");
					return;
				}
				if (compactUnavailable) {
					setSendError(compactUnavailable);
					return;
				}
				await onCompact();
				clearAcceptedDraft(composerRevision.current, mutationToken);
				mutationFinished = true;
				submitAccepted.current = true;
				setDismissedKey(null);
				setHighlighted(0);
			} catch {
				setSendError("Conversation history could not be compacted. Try again.");
			} finally {
				if (draftScope && mutationToken && !mutationFinished) {
					cancelChatComposerMutation(draftScope, mutationToken);
				}
				setSubmitting(false);
			}
			return;
		}

		await fileAttachments.toSettledPayload();
		if (fileAttachments.hasPendingReads()) return;
		const settledAttachments = fileAttachments.getAttachments();
		const settledPaths = settledAttachments.flatMap((attachment) =>
			attachment.stagedPath ? [attachment.stagedPath] : []);
		const attachedPaths = [
			...visibleRetainedAttachments.flatMap((attachment) => attachment.path ? [attachment.path] : []),
			...settledPaths,
		];
		editor.current?.pruneImages(attachedPaths);
		currentText = textRef.current;
		body = proseBesideImages(currentText.trim(), attachedPaths);
		if (!recoveringDelivery) {
			const tokens = editor.current?.getTokens() ?? [];
			if (tokens.length > 0) {
				recordSentComposerTokens(boundarySessionId ? sessionTokenScope(boundarySessionId) : messageTokenScope(body), tokens);
			}
		}
		const hasAttachments = settledAttachments.length > 0 || visibleRetainedAttachments.length > 0;
		if (attachmentsRefusal && settledAttachments.length > 0 && !recoveringDelivery) return;
		const canSubmitNow =
			(body.length > 0 || hasAttachments || contextReferences.length > 0 || Boolean(recoveringDelivery)) &&
			(!busy || savingQueuedEdit || recoveringDelivery?.state === "accepted") &&
			!disabled && !steerPending && !savingQueuedEditPending &&
			!composerMutation.pending &&
			(!draftMutationPending || Boolean(recoveringDelivery)) &&
			(Boolean(recoveringDelivery) ||
				getChatComposerMutation(draftScope ?? "").accepted?.result.ok !== false);
		if (!canSubmitNow) {
			if ((busy || draftMutationPending) && !composerMutation.pending && !disabled && !recoveringDelivery) {
				holdOwnDraft(forceSteer);
			}
			return;
		}
		const heldPayloads = holdAttachments
			? settledAttachments.flatMap(({ stagedPath, mimeType, data, name }) => !stagedPath && data ? [{ mimeType, data, name }] : [])
			: [];
		if (hasAttachments && settledPaths.length + heldPayloads.length !== settledAttachments.length) {
			setSendError("chat.draft.filesUnavailable");
			return;
		}
		const sendPayloads = (native: FileAttachmentPayload[]) => {
			const all = [...native, ...heldPayloads];
			return all.length > 0 ? all : undefined;
		};
		const shouldSteer = Boolean(forceSteer && !savingQueuedEdit && contextReferences.length === 0);
		rememberSentAttachments(settledAttachments.flatMap(({ stagedPath, mimeType, bytes, file, data }) =>
			stagedPath ? [{ path: stagedPath, mimeType, bytes, file, data }] : []));
		const message =withAttachmentReferences(body, attachedPaths) || (contextReferences.length > 0 ? `Use the attached ${contextReferences.length} ${fileReferences.length ? "annotation(s)" : "chat excerpt(s)"} as context` : "");
		const attachmentScope = queuedDraftScope ?? draftScope;
		const retainedNativeImageCount = visibleRetainedAttachments.filter((item) => item.contentType === "image").length;
		let nativeImageBytes = 0;
		const deliveryAttachments = recoveringDelivery?.draft?.attachments.map<FileAttachment>((attachment) =>
			settledAttachments.find((staged) => staged.id === attachment.id) ?? {
				...attachment, stagedPath: attachment.path,
			});
		const nativeImageAttachments = (deliveryAttachments ?? settledAttachments).filter((attachment) => {
			if (!sendNativeImages || retainedNativeImageCount > 0 || !isSupportedImageAttachment(attachment.mimeType) ||
				attachment.bytes > MAX_NATIVE_IMAGE_BYTES ||
				nativeImageBytes + attachment.bytes > MAX_NATIVE_IMAGES_BYTES) return false;
			nativeImageBytes += attachment.bytes;
			return true;
		});
		let nativePayloads = nativeImageAttachments.flatMap(({ mimeType, data }) =>
			data ? [{ mimeType, data }] : []);
		const restoreNativePayloads = async (): Promise<boolean> => {
			if (!attachmentScope || !sendNativeImages || recoveringDelivery?.kind === "steer" || recoveringDelivery?.state === "accepted") return true;
			const restored: FileAttachmentPayload[] = [];
			try {
				for (const attachment of nativeImageAttachments) {
					if (attachment.data) {
						restored.push({ mimeType: attachment.mimeType, data: attachment.data });
						continue;
					}
					if (!attachment.stagedPath) throw new Error("Missing staged attachment");
					const assetOrigin = remoteHost ? assetBaseUrl : assetBaseUrl ?? getApiBaseUrl();
					if (assetOrigin === undefined) throw new Error("Remote host disconnected");
					const response = await fetch(attachmentURL(assetOrigin, assetSessionId ?? attachmentScope.sessionId, attachment.stagedPath));
					if (!response.ok) throw new Error("Could not read staged attachment");
					const blob = await response.blob();
					const data = await new Promise<string>((resolve, reject) => {
						const reader = new FileReader();
						reader.onload = () => resolve(String(reader.result).split(",")[1] ?? "");
						reader.onerror = () => reject(new Error("Could not read staged attachment"));
						reader.readAsDataURL(blob);
					});
					restored.push({ mimeType: attachment.mimeType, data });
				}
				nativePayloads = restored;
				return true;
			} catch {
				setSendError("chat.draft.readAttachmentFailed");
				return false;
			}
		};
		if ((!draftScope || savingQueuedEdit) && !await restoreNativePayloads()) return;

		if (savingQueuedEdit && nativePayloads.length + retainedNativeImageCount > MAX_ATTACHMENTS) {
			setSendError(`You can attach up to ${MAX_ATTACHMENTS} images.`);
			return;
		}
		if (!draftScope || savingQueuedEdit) {
			setSubmitting(true);
			const clearForLocalEcho = !shouldSteer && !savingQueuedEdit && nativePayloads.length === 0;
			try {
				if (clearForLocalEcho) clearEditorView();
				if (shouldSteer && onSteer) {
					const outcome = nativePayloads.length > 0
						? await onSteer(message, nativePayloads)
						: await onSteer(message);
					if (outcome?.status === "not-accepted") {
						setSteerOutcomeNotice(outcome.reason);
						return;
					}
				} else if (savingQueuedEdit) {
					await onSend(message, nativePayloads.length ? nativePayloads : undefined, undefined,
						visibleRetainedAttachments.flatMap((item) => item.contentIndex === undefined ? [] : [item.contentIndex]));
					return;
				} else if (nativePayloads.length > 0 || heldPayloads.length > 0) {
					await onSend(message, sendPayloads(nativePayloads));
				} else {
					await onSend(message);
				}
				submitAccepted.current = true;
				if (!clearForLocalEcho) clearEditorView();
				fileAttachments.clear();
			} catch (error) {
				if (clearForLocalEcho && !textRef.current) {
					textRef.current = currentText;
					hasTextRef.current = currentText.trim().length > 0;
					setHasText(hasTextRef.current);
					editor.current?.setText(currentText);
				}
				setSendError(
					savingQueuedEdit
						? apiErrorMessage(error, "chat.draft.queueSaveFailed")
						: staged
						? "chat.draft.sendAttachmentsFailed"
						: "chat.draft.sendFailed",
				);
			} finally {
				setSubmitting(false);
			}
			return;
		}

  if ((recoveringDelivery?.fileReferences?.length || fileReferences.length) && (fileReferenceDelivery ?? (fileReferencesEnabled ? "structured" : "unsupported")) === "unsupported") {
   setSendError("chat.draft.fileReferencesUnsupported");
   return;
  }
		const sizeError = !recoveringDelivery && excerptSizeError(chatReferences);
		if (sizeError) {
			setSendError(sizeError);
			return;
		}
		const requestText = recoveringDelivery
			? recoveringDelivery.requestText
			: message;
		const capturedFiles = recoveringDelivery?.fileReferences ?? fileReferences.map(ref => ({ ...ref, referenceFormat: "numbered-v1" as const }));
		let capturedReferences = recoveringDelivery?.excerpts ?? chatReferences.map(upgradeExcerpt);
		if (!recoveringDelivery && (capturedReferences.length || capturedFiles.length)) {
			try {
				capturedReferences = fitExcerptContext(capturedReferences, requestText, messageByteLimit, capturedFiles);
			} catch (error) {
				setSendError(apiErrorMessage(error));
				return;
			}
			if (draftScope && JSON.stringify(capturedReferences) !== JSON.stringify(chatReferences)) {
				const saved = writeChatExcerptReferences(draftScope, capturedReferences);
				if (!saved.ok) {
					setTextDraftPersistenceError("chat.draft.saveFailed");
					return;
				}
			}
		}
		const prepared = prepareChatComposerDelivery(draftScope, {
			kind: recoveringDelivery?.kind ?? (shouldSteer ? "steer" : "send"),
			optimistic: !shouldSteer,
			nativeImages: sendNativeImages,
			composerText: currentText,
			attachments: settledAttachments.flatMap((attachment) =>
				attachment.stagedPath
					? [{
							id: attachment.id,
							path: attachment.stagedPath,
							name: attachment.name,
							mimeType: attachment.mimeType,
							bytes: attachment.bytes,
						}]
					: [],
			),
			excerpts: capturedReferences,
			fileReferences: capturedFiles,
			requestText,
			clientMessageId: recoveringDelivery?.clientMessageId ?? crypto.randomUUID(),
		});
		if (!prepared.ok) {
			setTextDraftPersistenceError(
				"chat.draft.prepareFailed",
			);
			return;
		}
		const delivery = prepared.mutation;
		if (!prepared.recovered && delivery.draft) clearEditorView();
		synchronouslyClearedDeliveryRevision.current = undefined;
		setDeliveryUncertain(false);
		if (!prepared.recovered) {
			composerRevision.current = prepared.draft.composer.revision;
			unsavedText.current = undefined;
		}
		setDurableDelivery(delivery);
		setTextDraftPersistenceError(null);
		setDeliveryRecoveryNotice(
			delivery.state === "accepted"
				? "chat.draft.acceptedMessage"
				: null,
		);
		if (delivery.state === "accepted") {
			acceptAndClearDurableDelivery(delivery);
			submitAccepted.current = true;
			return;
		}

		if (!mutationToken) return;
		let mutationFinished = false;
		setSubmitting(true);
		try {
			if (!await restoreNativePayloads()) return;
			if (!isChatComposerMutationCurrent(draftScope, mutationToken)) return;
			if (delivery.kind === "steer") {
				if (!onSteer) throw new Error("Steering is unavailable");
				const outcome = await onSteer(delivery.requestText, prepared.recovered || nativePayloads.length === 0 ? undefined : nativePayloads, delivery.clientMessageId, prepared.recovered);
				if (outcome?.status === "not-accepted") {
					const cleared = clearRejectedChatComposerDelivery(
						draftScope,
						delivery.clientMessageId,
						delivery.revision,
					);
					setDurableDelivery(cleared.draft.composer.delivery);
					composerRevision.current = cleared.draft.composer.revision;
					if (cleared.ok) {
						setTextDraftPersistenceError(null);
						setDeliveryRecoveryNotice(null);
						setSteerOutcomeNotice(outcome.reason);
					} else {
						setTextDraftPersistenceError(
							"chat.draft.clearRefusedSteerFailed",
						);
					}
					return;
				}
			} else {
				const deliveryAttachments = sendPayloads(sendNativeImages ? nativePayloads : []);
    if (delivery.fileReferences?.length) {
     if (fileReferenceDelivery === "text") {
      await onSend(formatCapturedFileReferences(delivery.requestText, delivery.fileReferences, delivery.fileReferenceFormat === "quoted-v1"), deliveryAttachments, delivery.clientMessageId, undefined, delivery.excerpts, delivery.fileReferences, delivery.requestText);
     } else {
      await onSend(delivery.requestText, deliveryAttachments, delivery.clientMessageId, undefined, delivery.excerpts, delivery.fileReferences);
     }
    } else if (delivery.excerpts?.length) {
     await onSend(delivery.requestText, deliveryAttachments, delivery.clientMessageId, undefined, delivery.excerpts);
    } else {
     await onSend(delivery.requestText, deliveryAttachments, delivery.clientMessageId);
    }
			}
			acceptAndClearDurableDelivery(delivery, mutationToken);
			mutationFinished = true;
			submitAccepted.current = true;
		} catch (error) {
			const refused = delivery.kind === "send" && !prepared.recovered &&
				DEFINITIVE_SEND_REJECTIONS.has(apiErrorCode(error) ?? "");
			const parks = parkUnsentSends && !refused && !discardedUnsentRef.current && fitsUnsentSlot(delivery, draftScope);
			if (delivery.kind === "send" && !parks && !textRef.current) {
				textRef.current = delivery.draft?.text ?? currentText;
				hasTextRef.current = textRef.current.trim().length > 0;
				setHasText(hasTextRef.current);
				editor.current?.setText(textRef.current);
			}
			if (refused) {
				const cleared = unsavedText.current
					? { ok: false, draft: readChatSessionDraft(draftScope) }
					: clearRejectedChatComposerDelivery(draftScope, delivery.clientMessageId, delivery.revision);
				setDurableDelivery(cleared.draft.composer.delivery);
				if (cleared.ok && !cleared.draft.composer.delivery) {
					composerRevision.current = cleared.draft.composer.revision;
					textRef.current = cleared.draft.composer.text;
					hasTextRef.current = textRef.current.trim().length > 0;
					setHasText(hasTextRef.current);
					editor.current?.setText(textRef.current);
				}
				setDeliveryUncertain(false);
				setDeliveryRecoveryNotice(null);
				setTextDraftPersistenceError(cleared.ok ? null : "chat.draft.saveFailed");
				setSendError(apiErrorCode(error) === AGENT_SWITCH_FAILED ? null : apiErrorMessage(error));
				return;
			}
			console.warn("[chat] send got no answer", { sessionId: draftScope.sessionId, code: apiErrorCode(error) ?? (error instanceof Error ? error.name : "unknown"), retry: prepared.recovered });
			if (parks) {
				if (prepared.recovered) setSendError("chat.draft.unsentRetryFailed");
				return;
			}
			setDeliveryUncertain(true);
			setDeliveryRecoveryNotice(
				delivery.kind === "steer"
					? "chat.draft.steerUncertain"
					: "chat.draft.sendUncertain",
			);
		} finally {
			if (!mutationFinished) cancelChatComposerMutation(draftScope, mutationToken);
			setSubmitting(false);
		}
	}

	submitRef.current = () => submit();
	const submitLatest = useRef(submit);
	submitLatest.current = submit;
	const heldSubmitReady = Boolean(
		heldSubmit &&
			!submitting &&
			!busy &&
			!disabled &&
			!steerPending &&
			!savingQueuedEdit &&
			!savingQueuedEditPending &&
			!draftMutationPending &&
			!acceptedClearFailed &&
			!composerMutation.pending &&
			!composerMutation.accepted &&
			!fileAttachments.preparing,
	);
	useEffect(() => {
		if (!heldSubmit || !heldSubmitReady || submitInFlight.current) return;
		setHeldSubmit(null);
		if (textRef.current !== heldSubmit.text) return;
		const parked = parkedUnsentId();
		if (parked && parked !== heldSubmit.unsentId) return;
		void submitLatest.current(undefined, heldSubmit.steer && canSteerDraft);
	}, [canSteerDraft, heldSubmit, heldSubmitReady]);

	function onEditorEnter(
		snapshot: ComposerEditorSnapshot,
		event: globalThis.KeyboardEvent,
	): boolean {
		textRef.current = snapshot.text;
		return handleEnterKey(event);
	}

	const handleEnterKey = useCallback(
		(event: globalThis.KeyboardEvent): boolean => {
			const liveSnapshot = editor.current?.getSnapshot();
			if (liveSnapshot) textRef.current = liveSnapshot.text;
			const liveTrigger = liveSnapshot?.trigger;
			const liveSuggestions = suggestionsFor(liveTrigger);
			if (liveSuggestions.length > 0) {
				if (textRef.current.trim() === "/compact" && onCompact) {
					void submit();
					return true;
				}
				triggerRef.current = liveTrigger;
				const chosen = liveSuggestions[Math.min(highlightedRef.current, liveSuggestions.length - 1)];
				if (chosen) pick(chosen.value);
				return true;
			}

			if (enterSends && canSteerNext && !textRef.current.trim() && !fileAttachments.hasPendingReads()) {
				if (
					(event.metaKey || event.ctrlKey) &&
					!event.repeat &&
					Date.now() - lastSubmitAt.current >= STEER_NEXT_AFTER_SEND_MS
				) {
					setSteerNextRequest((request) => request + 1);
				}
				return true;
			}
			const wantsSteer = enterSends && (event.metaKey || event.ctrlKey) && canSteerDraft;
			void submit(undefined, wantsSteer);
			return true;
		},
		[canSteerDraft, canSteerNext, enterSends, fileAttachments, messageByteLimit, onCompact, pick, suggestionsFor],
	);

	function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
		if (event.nativeEvent.isComposing || event.keyCode === 229) return;
		if (dictating && !event.metaKey && !event.ctrlKey && (event.key.length === 1 || event.key === "Backspace" || event.key === "Delete")) {
			dictation.detach();
		}
		const liveSnapshot = editor.current?.getSnapshot();
		if (liveSnapshot) textRef.current = liveSnapshot.text;
		const liveTrigger = liveSnapshot?.trigger;
		const liveSuggestions = suggestionsFor(liveTrigger);
		if (liveSuggestions.length > 0) {
			if (event.key === "ArrowDown" || event.key === "ArrowUp") {
				event.preventDefault();
				const next = moveHighlight(
					Math.min(highlightedRef.current, liveSuggestions.length - 1),
					event.key === "ArrowDown" ? 1 : -1,
					liveSuggestions.length,
				);
				highlightedRef.current = next;
				setHighlighted(next);
				return;
			}
			if (event.key === "Escape") {
				event.preventDefault();
				event.stopPropagation();
				dismissedKeyRef.current = liveTrigger?.key ?? null;
				setDismissedKey(dismissedKeyRef.current);
				return;
			}
		}

		if ((event.key === "ArrowUp" || event.key === "ArrowDown") && recallSentMessage(event.key === "ArrowUp", event)) {
			event.preventDefault();
			return;
		}

		if (event.key === "Escape" && editingQueuedTurnId && onCancelQueuedEdit && !queuedEditRecovery && !submitInFlight.current) {
			event.preventDefault();
			onCancelQueuedEdit();
		}
	}

	function recallSentMessage(older: boolean, event: KeyboardEvent<HTMLDivElement>): boolean {
		if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || isComposing) return false;
		if (editingQueuedTurnId || queuedEditRecovery || disabled || typingLocked || submitInFlight.current || draftMutationPending) {
			return false;
		}
		if (fileAttachments.preparing || fileAttachments.hasPendingReads()) return false;
		const caret = editor.current?.caretBoundary();
		const browsing = historyBrowse.current;
		if (!caret || !(caret.atStart || (browsing && !older && caret.atEnd))) return false;
		const browse = browsing ?? (older && sentMessages?.length
			? { entries: [...sentMessages].reverse(), index: -1, draft: textRef.current, shown: textRef.current }
			: null);
		if (!browse) return false;
		const index = browse.index + (older ? 1 : -1);
		if (index >= browse.entries.length) return false;
		editor.current?.setText(index < 0 ? browse.draft : browse.entries[index]!, "start");
		const snapshot = editor.current?.getSnapshot();
		historyBrowse.current = index < 0 || !snapshot ? null : { ...browse, index, shown: snapshot.text };
		if (snapshot) onEditorChange(snapshot);
		return true;
	}

	function removeImageFromSavedDraft(path: string) {
		const browse = historyBrowse.current;
		if (!browse) return;
		const draft = removeInlineImagePath(browse.draft, path);
		if (draft === browse.draft) return;
		historyBrowse.current = { ...browse, draft };
		persistComposerText(draft);
	}

	function attachFiles(files: File[]) {
		const reservation = proseBesideImages(textRef.current.trim(), composerImages.map((image) => image.path)) &&
			files.some((file) => file.type.startsWith("image/"))
			? editor.current?.reserveImages()
			: undefined;
		void fileAttachments.addFiles(files).then((added) => {
			if (!reservation) return;
			editor.current?.fillImages(reservation, added.flatMap((attachment) =>
				attachment.stagedPath && isInlineImagePath(attachment.stagedPath) ? [attachment.stagedPath] : []));
		});
	}

	function onPaste(event: ClipboardEvent<HTMLDivElement>) {
		if (!canAttach || fileAttachments.preparing || draftMutationPending || submitInFlight.current) return;
		const clipboard = event.clipboardData;
		const files = Array.from(clipboard?.files ?? []);
		if (files.length === 0) return;
		const hasText = typeof clipboard?.getData === "function" && clipboard.getData("text/plain") !== "";
		if (!hasText) event.preventDefault();
		attachFiles(files);
	}

	function onDrop(event: DragEvent<HTMLFormElement>) {
		setDragging(false);
		if (!canAttach || fileAttachments.preparing || draftMutationPending || submitInFlight.current) return;
		const files = Array.from(event.dataTransfer?.files ?? []);
		if (files.length === 0) return;
		event.preventDefault();
		event.stopPropagation();
		attachFiles(files);
	}

	useEffect(() => {
		const onKey = (event: globalThis.KeyboardEvent) => {
			setModifierHeld(event.metaKey || event.ctrlKey);
		};
		const onBlur = () => {
			setModifierHeld(false);
		};
		window.addEventListener("keydown", onKey);
		window.addEventListener("keyup", onKey);
		window.addEventListener("blur", onBlur);
		return () => {
			window.removeEventListener("keydown", onKey);
			window.removeEventListener("keyup", onKey);
			window.removeEventListener("blur", onBlur);
		};
	}, []);

	const attachmentError =
		fileAttachments.error ??
		draftPersistenceError ??
		(optimisticSend ? null : deliveryRecoveryNotice) ??
		(attachmentsRefusal && fileAttachments.attachments.length > 0 && !durableDelivery ? attachmentsRefusal : null) ??
		sendError ??
		(fileAttachments.attachments.some((file) => !file.data && !file.stagedPath)
			? "chat.draft.filesUnavailable" : null) ??
		commandError;
	const retryingDelivery = retryingUnsent && durableDelivery?.clientMessageId === retryingUnsent ? durableDelivery : undefined;
	const stripDelivery = unsent ?? retryingDelivery;
	const unsentStrip = stripDelivery?.draft ? (
		<UnsentMessageStrip
			text={stripDelivery.draft.text}
			attachmentCount={stripDelivery.draft.attachments.length}
			sending={!unsent}
			retryDisabled={Boolean(busy || disabled || typingLocked || sendBlockedReason || durableDelivery || submitting || composerMutation.pending)}
			onRetry={retryUnsent}
			onDiscard={discardUnsent}
		/>
	) : discardedUnsent ? (
		<UnsentMessageStrip discarded onUndo={undoDiscardUnsent} />
	) : null;
	const elicitationEnters = useDockEntrance(elicitation ? String((isValidElement(elicitation) && elicitation.key) || "elicitation") : null);
	const unsentEnters = useDockEntrance(unsentStrip ? (stripDelivery?.draft ? "unsent" : "discarded") : null);
	const withQueueStack = (form: ReactElement) =>
		(
			<div className="relative mx-auto flex w-full max-w-3xl flex-col">
				{queuedDock ? (
				<div
					className="cursor-chat-composer-queue relative z-10 mx-auto mb-2 w-[calc(100%-2rem)]"
					data-testid="queued-composer-dock"
				>
					{queuedDockWithSteer}
				</div>
				) : null}
				{elicitation ? (
					<div
						className={cn("relative z-10 mx-auto mb-2 w-[calc(100%-2rem)]", elicitationEnters && "queue-dock-enter")}
						data-testid="elicitation-composer-dock"
						data-dock-quiet={elicitationEnters ? undefined : ""}
					>
						{elicitation}
					</div>
				) : null}
				{unsentStrip ? (
					<div className={cn("relative z-10 mx-auto mb-2 w-[calc(100%-2rem)]", unsentEnters && "queue-dock-enter")}>
						{unsentStrip}
					</div>
				) : null}
				<ComposerBand band={trayBand} />
				{notice}
				{form}
			</div>
		);

	if (approval) {
		return withQueueStack(
			<form
				onSubmit={(event) => event.preventDefault()}
				className="cursor-chat-composer relative flex flex-col gap-1.5 px-3 py-3"
			>
				{approval}
				{commandError ? (
					<p role="alert" className="px-1.5 text-xs leading-snug text-destructive">
						{commandError}
					</p>
				) : null}
			</form>,
		);
	}

	return withQueueStack(
		<form
			onSubmit={(event) => void submit(event, steersDraft)}
				onDragOver={(event) => {
					if (!canAttach || submitInFlight.current) return;
					event.preventDefault();
					setDragging(true);
				}}
				onDragLeave={() => setDragging(false)}
				onDropCapture={onDrop}
				data-dragging={dragging || undefined}
				data-starting={starting || undefined}
				aria-busy={starting || undefined}
				onMouseDown={(e) => {
					if (controlsDisabled || e.button !== 0) return;
					if (!e.currentTarget.contains(e.target as Node)) return;
					if ((e.target as HTMLElement).closest('[contenteditable="true"]')) return;
					if (
						e.target === e.currentTarget ||
						!(e.target as HTMLElement).closest("button, a, [role='option'], [data-context-meter], ul")
					) {
						e.preventDefault();
						editor.current?.focus();
					}
				}}
				className="cursor-chat-composer relative flex cursor-text flex-col gap-1.5 px-3 pt-3 pb-3"
			>
				<span aria-hidden="true" className="chat-composer-startup-shimmer" />
				{menuOpen && trigger ? (
					<ComposerSuggestMenu
						id={menuId}
						kind={trigger.kind}
						items={suggestions}
						highlighted={activeIndex}
						onPick={pick}
						truncated={trigger?.kind === "file" && filePathsTruncated}
					/>
				) : null}

				{editingQueuedTurnId ? (
					<div className="flex items-center justify-between text-xs text-muted-foreground">
						<span>Editing queued message</span>
						<Button
							type="button"
							variant="quiet"
							size="xs"
							disabled={controlsDisabled || queuedEditRecovery}
							onClick={onCancelQueuedEdit}
						>
							Cancel edit
						</Button>
					</div>
				) : null}
				{(staged && !stripConcealed) || pendingAttachments.length > 0 || contextReferences.length > 0 ? (
					<div className="flex flex-wrap items-center gap-1.5" data-testid="composer-attachments-and-annotations">
						{(staged && !stripConcealed) || pendingAttachments.length > 0 ? (
							<ul className="contents" aria-label="Attached files">
								{stripAttachments.map(({ file, path, preview }) => {
									return (
									<li
										key={file.id}
										className="flex items-center gap-1.5 rounded-md bg-interactive-hover py-0.5 pl-0.5 pr-1"
									>
										{preview ? (
											<AttachmentThumbnail src={preview} name={file.name} />
										) : (
											<div className="flex size-6 items-center justify-center rounded-sm bg-surface">
												<File aria-hidden="true" className="size-3.5 text-muted-foreground" />
											</div>
										)}
										{preview ? (
											<span className="max-w-[120px] truncate text-xs text-muted-foreground" title={file.name}>
												{file.name}
											</span>
										) : (
											<span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground" title={file.name}>
												<AttachmentFileText name={file.name} bytes={"bytes" in file ? file.bytes : undefined} nameClassName="max-w-[120px]" />
											</span>
										)}
										<Button
											type="button"
											variant="destructive-ghost"
											size="icon-xs"
											onMouseDown={(event) => {
												if (event.button === 0) event.preventDefault();
											}}
											onClick={() => {
											if (submitInFlight.current) return;
											if (path) {
											editor.current?.removeImage(path);
											removeImageFromSavedDraft(path);
										}
											if (visibleRetainedAttachments.some((attachment) => attachment.id === file.id)) {
												const next = retainedAttachments.filter((attachment) => attachment.id !== file.id);
												setRetainedAttachments(next);
												onQueuedRetainedAttachmentsChange?.(next);
											} else fileAttachments.remove(file.id);
											editor.current?.focus();
											}}
											disabled={controlsDisabled || queuedEditRecovery || draftMutationPending || fileAttachments.preparing}
											aria-label={`Remove ${file.name}`}
										>
											<X aria-hidden="true" className="size-3" />
										</Button>
									</li>
									);
								})}
								{pendingAttachments.map((file) => (
									<li
										key={file.id}
										role="status"
										aria-label={`Attaching ${file.name}`}
										className="flex items-center gap-1.5 rounded-md bg-interactive-hover py-0.5 pl-0.5 pr-1"
									>
										<div aria-hidden="true" className="size-6 rounded-sm bg-surface motion-safe:animate-pulse" />
										<span aria-hidden="true" className="relative max-w-[120px] truncate text-xs motion-safe:animate-pulse">
											<span className="invisible">{file.name}</span>
											<span className="absolute inset-x-0 top-1/2 h-2 -translate-y-1/2 rounded-sm bg-surface" />
										</span>
										<Button
											type="button"
											variant="destructive-ghost"
											size="icon-xs"
											onMouseDown={(event) => {
												if (event.button === 0) event.preventDefault();
											}}
											onClick={() => {
												fileAttachments.cancelPending(file.id);
												editor.current?.focus();
											}}
											aria-label={`Cancel attaching ${file.name}`}
										>
											<X aria-hidden="true" className="size-3" />
										</Button>
									</li>
								))}
							</ul>
						) : null}
						{contextReferences.length > 0 ? (
							<ChatAnnotationSummary
								annotations={contextReferences}
								onSelect={onSelectAnnotation}
								disabled={controlsDisabled || draftMutationPending}
								onRemoveAll={() => {
									if (!draftScope || submitInFlight.current) return;
         const removedFiles = writeChatFileReferences(draftScope, []);
         if (!removedFiles.ok) { setTextDraftPersistenceError("chat.draft.saveFailed"); return; }
         const result = writeChatExcerptReferences(draftScope, []);
									composerRevision.current = result.draft.composer.revision;
									setTextDraftPersistenceError(result.ok ? null : "chat.draft.saveFailed");
								}}
								onRemove={(excerpt) => {
									if (!draftScope || submitInFlight.current || !excerpt.id) return;
         if (fileReferences.some(ref => ref.id === excerpt.id)) {
          const result = writeChatFileReferences(draftScope, fileReferences.filter(ref => ref.id !== excerpt.id));
          composerRevision.current = result.draft.composer.revision;
          setTextDraftPersistenceError(result.ok ? null : "chat.draft.saveFailed"); return;
         }
									const result = writeChatExcerptReferences(
										draftScope,
										chatReferences.filter((item) => item.id !== excerpt.id),
									);
									composerRevision.current = result.draft.composer.revision;
									setTextDraftPersistenceError(result.ok ? null : "chat.draft.saveFailed");
								}}
							/>
						) : null}
					</div>
				) : null}

				<ComposerEditor
					ref={editor}
					dictating={dictating}
					images={composerImages}
					promptSuggestion={willQueue || starting || editingQueuedTurnId || staged ? undefined : promptSuggestion}
					disabled={Boolean(disabled || queuedEditRecovery || (!optimisticSend && (submitting || draftMutationPending)))}
					readOnly={typingLocked}
					enterSends={enterSends}
					concealed={sendConcealed && !optimisticSend}
					label="Message the agent"
					placeholder={
						disabledPlaceholder ?? (disabled
							? "The controller is not connected"
							: willQueue
								? (queuePlaceholder ?? "Next message you send will be queued")
								: emptyPlaceholder ?? "Message the agent")
					}
					menuOpen={menuOpen}
					menuId={menuId}
					activeIndex={activeIndex}
					onChange={onEditorChange}
					onComplete={completeFromEditor}
					onEnter={onEditorEnter}
					onCompositionChange={setIsComposing}
					onKeyDown={onKeyDown}
					onPaste={onPaste}
				/>

				{attachmentError ? (
					<p role="alert" className="px-1.5 text-xs leading-snug text-destructive">
						{translateDraft(attachmentError)}
					</p>
				) : null}
				{canAbandonUncertainSteer ? (
					<div className="px-1.5">
						<Button
							type="button"
							variant="secondary"
							size="sm"
							onClick={abandonUncertainSteer}
						>
							{translateDraft("chat.draft.abandon")}
						</Button>
					</div>
				) : null}

				{dictation.error ? (
					<p role="status" className="px-1.5 text-xs leading-snug text-warning">
						{dictation.error}
					</p>
				) : null}
				{steerRefusal ?? steerOutcomeNotice ? (
					<p role="status" className="px-1.5 text-xs leading-snug text-warning">
						{translateDraft(steerRefusal ?? steerOutcomeNotice)}
					</p>
				) : null}

				<div className="flex h-7 items-center gap-1.5">
					<div role="group" aria-label="Message tools" className="flex min-w-0 flex-1 items-center gap-0.5">
						{canAttach ? (
							<>
								<input
									ref={filePicker}
									type="file"
									multiple
									hidden
									disabled={controlsDisabled}
									onChange={(event) => {
										if (!disabled && !submitInFlight.current && !draftMutationPending && !fileAttachments.preparing) {
											attachFiles(Array.from(event.target.files ?? []));
										}
										event.target.value = "";
									}}
								/>
								{addMenuItems ? (
									<OptionMenu
										onOpenChange={(open) => {
											const button = addMenuTrigger.current;
											const box = button?.closest("form")?.getBoundingClientRect();
											const origin = button?.getBoundingClientRect();
											if (open && box && origin) setAddMenuBox({ lift: origin.top - box.top + 6, shift: box.left - origin.left, width: box.width });
										}}
									>
										<OptionMenuTrigger variant="ghost"
											ref={addMenuTrigger}
											showCaret={false}
											disabled={controlsDisabled}
											aria-label="Add"
											title="Add files or change mode"
											className={cn("size-7 shrink-0 justify-center rounded-full bg-transparent p-0 text-muted-foreground hover:text-foreground data-[state=open]:text-foreground", GHOST_PILL_CLASS)}
										>
											<Plus aria-hidden="true" className="size-3.5" />
										</OptionMenuTrigger>
										<OptionMenuContent
											align="start"
											side="top"
											sideOffset={addMenuBox?.lift}
											alignOffset={addMenuBox?.shift}
											avoidCollisions={false}
											style={addMenuBox ? { width: addMenuBox.width } : undefined}
											className="chat-settings-menu max-w-none text-[12px]!"
										>
											<OptionMenuLabel>Add</OptionMenuLabel>
											<ComposerMenuRow
												icon={Paperclip}
												title="Files"
												description="Attach files to this message"
												disabled={queuedEditRecovery || draftMutationPending || fileAttachments.preparing}
												onSelect={() => filePicker.current?.click()}
											/>
											{addMenuItems}
										</OptionMenuContent>
									</OptionMenu>
								) : (
								<span className="inline-flex">
									<Button
										type="button"
										variant="quiet"
										size="icon-sm"
										shape="pill"
										disabled={controlsDisabled || queuedEditRecovery || draftMutationPending || fileAttachments.preparing}
										onClick={() => filePicker.current?.click()}
										aria-label="Attach a file"
										className="shrink-0"
									>
										<Plus aria-hidden="true" className="size-3.5" />
									</Button>
								</span>
								)}
							</>
						) : null}
						{settings}
					</div>

					<div role="group" aria-label="Send message controls" className="flex h-7 shrink-0 items-center">
						{showSteerButton && canSteerDraft && (hasText || staged) ? (
							<Button type="button" variant="ghost" size="sm" disabled={!sendActionEnabled} onClick={() => void submit(undefined, true)} aria-label="Steer into running turn">
								Steer
							</Button>
						) : null}
						{canDictate ? (
							<Tooltip>
								<TooltipTrigger asChild>
									<span className="inline-flex">
										<Button
											type="button"
											variant={dictating ? "primary" : "quiet"}
											size="none"
											shape="pill"
											disabled={!dictating && (controlsDisabled || typingLocked)}
											onMouseDown={(event) => event.preventDefault()}
											onClick={() => {
												editor.current?.focus();
												dictation.toggle();
											}}
											data-shortcut-target="dictate"
											aria-label={dictating ? "Finish dictation" : "Dictate"}
											aria-pressed={dictating}
											className={cn(
												"mr-1 h-7 overflow-hidden p-0 transition-[width,background-color,color] duration-200 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none",
												dictating ? "w-[76px]" : "w-7",
											)}
										>
											{dictating ? (
												<DictationMeter listening={dictation.phase === "listening"} />
											) : (
												<Mic aria-hidden="true" className="size-3.5" />
											)}
										</Button>
									</span>
								</TooltipTrigger>
								<TooltipContent side="bottom">{dictating ? "Finish dictation" : "Dictate"}</TooltipContent>
							</Tooltip>
						) : null}
						<Tooltip>
							<TooltipTrigger asChild>
								<span className="inline-flex">
									<Button
										type={canStopTurn || canContinueTurn ? "button" : "submit"}
										variant="primary"
										size="icon-sm"
										shape="pill"
										disabled={canStopTurn ? false : canContinueTurn ? continuing : !sendActionEnabled}
										onClick={canStopTurn ? onInterrupt : canContinueTurn ? continueTurn : undefined}
										aria-label={canStopTurn ? "Stop turn" : canContinueTurn ? "Continue turn" : sendActionLabel}
									>
										{canStopTurn ? (
											<Square aria-hidden="true" className="size-2.5 fill-current" />
										) : canContinueTurn && !continuing ? (
											<Play aria-hidden="true" className="size-3 translate-x-px fill-current" />
										) : continuing || (!starting && (submitting || steerPending || savingQueuedEditPending || sendPending)) ? (
											<Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
										) : showsSteer ? (
											<CornerUpRight aria-hidden="true" className="size-3.5" />
										) : (
											<ArrowUp aria-hidden="true" className="size-3.5" />
										)}
									</Button>
								</span>
							</TooltipTrigger>
							<TooltipContent side="bottom">{canStopTurn ? "Stop turn" : canContinueTurn ? "Continue from where it stopped" : sendBlockedReason ?? (durableDelivery ? sendActionLabel : sendHint)}</TooltipContent>
						</Tooltip>
					</div>
				</div>
			</form>,
	);
});
