import AsyncStorage from "@react-native-async-storage/async-storage";
import type { ServerConfig } from "../config";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { useCallback, useEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode } from "react";
import { switchOffersFast } from "../agentSwitch";
import { ActivityIndicator, Keyboard, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import Svg, { Defs, LinearGradient, Rect, Stop } from "react-native-svg";
import Animated, { FadeIn, FadeOut, interpolate, measure, useAnimatedReaction, useAnimatedRef, useAnimatedStyle, useFrameCallback, useSharedValue, withSpring, withTiming, type SharedValue } from "react-native-reanimated";
import { useReanimatedFocusedInput, useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { useComposerDockRise } from "./composerDock";
import { rememberChatSettings } from "./chatSettingsTemplate";
import { fastPendingKey, rememberFastModel, usePendingFast } from "./learnedFast";
import { turnSettingsParts } from "./turnSettingsModel";
import { KeyboardMotionScope, useKeyboardMotion } from "./keyboardMotion";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { fontScaleCap, iconSize, radius, space, stateOpacity, touchTarget, type } from "../tokens";
import { Button } from "../ui";
import { useReducedMotion } from "../useReducedMotion";
import { DictationField } from "../voice/DictationField";
import { useVoiceInput } from "../voice/useVoiceInput";
import { activeTurn, type ChatConfigOption, type ChatExcerpt, type ChatImage, type ChatModel, type ChatResource, type ChatSkill, type ConversationSnapshot, type TurnSettings } from "./types";
import {
	findComposerSuggestion,
	rankComposerCatalog,
	replaceComposerSuggestion,
	type ComposerPickerCatalog,
	type ComposerSuggestion,
} from "./composerSuggestions";
import { ComposerSuggestionPanel } from "./ComposerSuggestionPanel";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { ComposerAttachmentTray } from "./ComposerAttachmentTray";
import { ComposerGlass, composerGlassSupported } from "./composer-glass";
import { COMPOSER_DISC, COMPOSER_FIELD_HEIGHT, COMPOSER_FONT_SCALE_CAP, COMPOSER_HEIGHT, COMPOSER_RADIUS, composerStyleRules, useComposerFieldSize } from "./composerStyles";
import { ChatTurnSettingsControl } from "./ChatTurnSettingsControl";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { ComposerField } from "../composer-field";
import { EffortSliderHost } from "../effort-slider-host";
import { effortGaugeToggles } from "../effort-gauge";
import { ModelWheelHost } from "../model-wheel";
import { PermissionPickerHost } from "../permission-picker";
import { useChatEffort } from "./useChatEffort";
import { isSentDraft, ULTRACODE_HARNESS, ultracodeKey, withUltracode } from "./ultracode";
import { useChatModelWheel } from "./useChatModelWheel";
import { useChatPermissions } from "./useChatPermissions";
import { compactCount } from "../files/model";
import { composerDeliveryPresentation, composerDeliveryRoute, composerPrimaryAction, hardwareReturnIntent, type ComposerDeliveryIntent } from "./composerDeliveryModel";
import { HardwareReturn } from "./HardwareReturn";
import { useKeyboardSendRefocus } from "./useKeyboardSendRefocus";
import { contextMeterModel, quotaChipModel } from "./contextMeter";
import { RequestCard } from "./RequestCard";
import type { RequestDockModel } from "./requestDockModel";
import { createRequestGate } from "./requestGate";
import { queuedConversationMessages } from "./timelineModel";
import { userFacingError } from "../connectionError";
import type { DashboardPR, SessionPRSummary } from "../api";
import { PRChipContent, prChipAccessibilityLabel } from "./PRChip";
import { decodeResponseDraft, encodeResponseDraft, responseReplyText, responseExcerpts, isChatExcerpt, type ResponseQuote, type ResponseQuoteRequest } from "./responseQuotes";
import { afterDraftWrites } from "./pendingSend";
import { composerTrayItems, forgetComposerAttachments, readComposerAttachments, removeComposerAttachment, updateComposerAttachments, useComposerAttachments, type ComposerAttachment as Attachment } from "./composerAttachments";
import { attachComposerImages, MAX_ATTACHMENTS } from "./composerImages";
import { useImagePaste } from "./useImagePaste";

const MAX_EMBEDDED_FILE_BYTES = 500_000;

/**
 * The pill's corner controls (attach, and the round button with the mic beside
 * it while a turn runs) are this wide with the gap that sets them off the field.
 */
const CORNER_CONTROL = touchTarget + space.xxs;
/**
 * The pill opening and closing: one spring that grows the controls row under the
 * field and lets the field out from between the corner controls. Settles quickly
 * with a little life, like the system's own sheet detents.
 */
const COMPOSER_SPRING = { damping: 22, stiffness: 260, mass: 0.9 } as const;
/** How long the field row is watched after it changes: past the pill's spring and a line's layout. */
const FIELD_WATCH_MS = 500;
/** The collapsed composer's chips are this tall; their slop makes up the rest of a touch target. */
const CHIP_HEIGHT = space.xxxl;
const CHIP_SLOP = (touchTarget - CHIP_HEIGHT) / 2;
/** The chips and the card's controls fade as the composer opens and closes. */
const COMPOSER_FADE_MS = 160;
/** How far above the dock the conversation starts fading out behind it. */
const DOCK_FADE_RISE = space.xxl;
/** The fade's gradient: from that height down past the chips, `DOCK_FADE_FLOOR` below. */
const DOCK_FADE_HEIGHT = DOCK_FADE_RISE + CHIP_HEIGHT;
/** The fade's cover below the chips, the bars' frost at the screen edge (see `frost`). */
const DOCK_FADE_FLOOR = 0.92;

// The composer's one-off glyphs.
/** Redirect the running turn now. */
const steer = { ios: "arrow.turn.up.right", android: "turn_right" } as const;
/** A message waiting its turn in the queue. */
const queued = { ios: "arrow.turn.down.right", android: "subdirectory_arrow_right" } as const;
/** Sent once the running turn ends. */
const approvalGlyph = { ios: "shield", android: "shield" } as const;
/** Dictate, filled as on the new-task sheet's round button. */
const dictateGlyph = { ios: "mic.fill", android: "mic" } as const;
/** The field's text inset once the pill has opened: just clear of the card's rounded corner. */
const OPEN_FIELD_INSET = space.xs;
// The waveform's inset from the pill's leading edge while the mic is open.
const DICTATION_INSET = space.md;

/** The chat's composer, sharing the screen's keyboard handler when there is one (see KeyboardMotionScope). */
export function ChatComposer(props: ComponentProps<typeof Composer>) {
	return <KeyboardMotionScope><Composer {...props} /></KeyboardMotionScope>;
}

function Composer({
	quoteRequest,
	onQuoteHandled,
	reviewPR,
	reviewPRSummary,
	onOpenReview,
	onExpandedChange,
	sessionId,
	config,
	machine,
	changes,
	device,
	snapshot,
	skills,
	filePaths,
	filePathsTruncated,
	onLoadSkills,
	onLoadFiles,
	configOptions,
	models,
	steerUnavailable,
	pending,
	interrupting,
	disabled,
	onSend,
	onAcknowledgeSend,
	completedRetry,
	onSteer,
	onPromoteQueuedTurn,
	onCancelQueuedTurn,
	onInterrupt,
	onSettings,
	onConfigOption,
	otherAgents,
	restingInset,
	dockRiseOverride,
	openDeficit,
	fieldGrowth,
	onOpenUsage,
	request,
	requestDismissed,
	onRequestDecide,
	onRequestResolveInput,
	onShowRequest,
	onDismissRequest,
	onRestoreRequest,
	openingTurnId,
	startingAgentName,
	fileUploads,
	attachmentsUnavailable,
	cloud = false,
}: {
	quoteRequest?: ResponseQuoteRequest;
	onQuoteHandled?(id: string): void;
	reviewPR?: DashboardPR;
	reviewPRSummary?: SessionPRSummary;
	/** Opens the PR's review, from its chip. */
	onOpenReview?(): void;
	/** Told as the pill opens and closes, so the screen can clear what floats over it. */
	onExpandedChange?(expanded: boolean): void;
	sessionId: string;
	config: ServerConfig | null;
	/** The store's `machine`: keys the draft and attachments, which must survive the connection dropping. */
	machine: string;
	/** What this session changed, from git. Present only when it changed something. */
	changes?: { files: number; additions: number; deletions: number; onOpen(): void };
	/** The simulator or emulator this session holds, when it holds one; opens its screen. */
	device?: { name: string; onOpen(): void };
	snapshot: ConversationSnapshot;
	skills: ChatSkill[];
	filePaths: string[];
	filePathsTruncated?: boolean;
	onLoadSkills(): Promise<ChatSkill[]>;
	onLoadFiles(): Promise<{ paths: string[]; truncated: boolean }>;
	configOptions?: ChatConfigOption[];
	models: ChatModel[];
	steerUnavailable?: boolean;
	pending?: boolean;
	interrupting?: boolean;
	disabled?: boolean;
	/** `typed` is the draft as typed, where `text` adds Ultracode's prefix to it. */
	onSend(text: string, attachments?: ChatImage[], resources?: ChatResource[], excerpts?: ChatExcerpt[], typed?: string): Promise<string>;
	onAcknowledgeSend(id: string): Promise<void>;
	completedRetry?: { id: string; draftText: string };
	onSteer(text: string, typed?: string): Promise<string>;
	/** Steers with a queued message. Left out where the host can't, which hides the action. */
	onPromoteQueuedTurn?(turnId: string): Promise<void>;
	/** Deletes a queued message. Left out where the host can't, which hides the action. */
	onCancelQueuedTurn?(turnId: string): Promise<void>;
	onInterrupt(): void;
	onSettings(settings: TurnSettings): Promise<void>;
	onConfigOption(id: string, value: { value: string } | { enabled: boolean }): Promise<ChatConfigOption[]>;
	/** The model menu's other agents; choosing one's model switches the session's agent. */
	otherAgents?: ChatTurnSettingsControlProps["otherAgents"];
	/**
	 * What the dock owes with the keyboard down — the home-indicator inset. The
	 * dock keeps it at all times and closes the difference to the keyboard by
	 * itself; see `dockRise` below.
	 */
	restingInset: number;
	/**
	 * Pins the dock's rise (0 resting, 1 against the keyboard) when the keyboard's
	 * own progress missed a transition and is stuck at the old end. Leave it
	 * unset to ride the progress. See `chatDockRiseOverride`.
	 */
	dockRiseOverride?: number;
	/** How much shorter the pill is than open, so a screen can always pad for it open. */
	openDeficit?: SharedValue<number>;
	/** How much taller the field row is than one empty line, on the UI thread. */
	fieldGrowth?: SharedValue<number>;
	/** Opens the context & usage sheet, from the quota chip. */
	onOpenUsage?(): void;
	/** A pending request, which takes the composer's place until it is answered. */
	request?: RequestDockModel | null;
	/** The user pushed the request aside to type instead. */
	requestDismissed?: boolean;
	onRequestDecide(requestId: string, decisionId: string): Promise<void>;
	onRequestResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	onShowRequest(sequence: number): void;
	onDismissRequest(): void;
	onRestoreRequest(): void;
	/** A starting session's brief, which the timeline shows as sent. */
	openingTurnId?: string;
	/** Set while the session starts, for the placeholder. */
	startingAgentName?: string;
	/**
	 * Files of any type go with the message as uploads, up to this size each,
	 * instead of being embedded as text: an Orchestrator Cloud session takes
	 * them that way. A picked file is then sent as a resource with only its
	 * local `uri`, which the screen reads and uploads when the message is sent.
	 */
	fileUploads?: { maxBytes: number };
	/** Nothing can be attached here (an organization whose plan takes no files): attach stays in place, dimmed. */
	attachmentsUnavailable?: boolean;
	/** An Orchestrator Cloud chat, which has no Ultracode, as on desktop. */
	cloud?: boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const fieldSize = useComposerFieldSize();
	const iconFeedback = usePressFeedback("icon");
	const buttonFeedback = usePressFeedback("button");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	// The keyboard's own progress, 0 closed to 1 open. This is the same value the
	// keyboard is animating with, so the dock moves in lockstep with it.
	const keyboard = useReanimatedKeyboardAnimation();
	const reviewPromptAvailable = Boolean(reviewPR && onOpenReview);
	// Every docked composer rides the keyboard the same way (see composerDock).
	const dockRise = useComposerDockRise(restingInset, dockRiseOverride);
	const [text, setText] = useState("");
	const [quotes, setQuotes] = useState<ResponseQuote[]>([]);
	const inputRef = useRef<TextInput>(null);
	const consumedQuote = useRef<string | undefined>(undefined);
	const [draftLoaded, setDraftLoaded] = useState(false);
	// Undefined until the field reports a selection: text set from outside (a
	// restored draft, a transcript) leaves the native caret at its end without
	// a selection event, so an unknown caret reads as the end of the text.
	const [cursor, setCursor] = useState<number>();
	// Not component state: an Android pick can resolve after this composer has
	// unmounted (see composerAttachments.ts).
	const attachmentKey = machine ? `${machine}.${sessionId}` : sessionId;
	const { items: attachments, notice: attachmentNotice } = useComposerAttachments(attachmentKey);
	const clearAttachments = useCallback(() => updateComposerAttachments(attachmentKey, () => ({ items: [] })), [attachmentKey]);
	const trayItems = useMemo(() => composerTrayItems(attachments), [attachments]);
	const dismissAttachmentNotice = useCallback(() => updateComposerAttachments(attachmentKey, (current) => current.notice ? { items: current.items } : current), [attachmentKey]);
	const [localError, setLocalError] = useState<string>();
	const [submitting, setSubmitting] = useState(false);
	const [focused, setFocused] = useState(false);
	// Return on a hardware keyboard: typing carries on in the field after the send.
	const keyboardSend = useKeyboardSendRefocus(inputRef, submitting);
	const [promotingQueuedTurnId, setPromotingQueuedTurnId] = useState<string>();
	const [cancellingQueuedTurnId, setCancellingQueuedTurnId] = useState<string>();
	const [hiddenQueuedTurnIds, setHiddenQueuedTurnIds] = useState<Set<string>>(() => new Set());
	const active = Boolean(activeTurn(snapshot));
	const queuedMessages = useMemo(() => queuedConversationMessages(snapshot, openingTurnId), [openingTurnId, snapshot]);
	const visibleQueuedMessages = useMemo(() => queuedMessages.filter((entry) => !hiddenQueuedTurnIds.has(entry.turnId)), [hiddenQueuedTurnIds, queuedMessages]);
	// Absent below 70%: a permanent token gauge is chrome nobody reads.
	const contextMeter = contextMeterModel(snapshot.usage);
	// Absent below 75% of the account's usage limit, for the same reason.
	const quotaChip = quotaChipModel(snapshot.rateLimits);
	// A blocking question is the next thing to do, so it takes the input's place
	// rather than pointing at a card somewhere up the timeline.
	const requestCard = request && !requestDismissed ? (
		<RequestCard
			model={request}
			onDecide={onRequestDecide}
			onResolveInput={onRequestResolveInput}
			onShow={onShowRequest}
			onDismiss={onDismissRequest}
		/>
	) : null;
	const canSteer = snapshot.capabilities?.includes("steer") && !steerUnavailable && active;
	const canEmbedFiles = snapshot.capabilities?.includes("embedded_context");
	const voice = useVoiceInput({ onTranscript: useCallback((spoken: string) => { setText((old) => old ? `${old} ${spoken}` : spoken); setCursor(undefined); }, []) });
	const hasDraft = Boolean(text.trim() || quotes.length);
	const listening = voice.state === "starting" || voice.state === "recording";
	const transcribing = voice.state === "transcribing";
	const dictating = listening || transcribing;
	// Collapsed, the composer is a slim pill under the session's chips. Writing
	// grows it into a card that adds the turn settings between attach and the
	// round button, and it stays open while anything waits to be sent. While the
	// mic is open it is the slim pill again with only the waveform and the stop
	// button, so the trace gets the whole width.
	// `keyboardSend.holding` keeps it open through a hardware-Return send, while the
	// draft has cleared and the field has yet to get its focus back.
	const expanded = !dictating && (focused || keyboardSend.holding || hasDraft || attachments.length > 0);
	// 0 collapsed, 1 open. Everything that changes as the pill opens reads it, so
	// the row, the field's insets and the settings move as one.
	const reduceMotion = useReducedMotion();
	const openness = useSharedValue(expanded ? 1 : 0);
	// 1 while the mic is open: the attach button is gone and the field reaches the pill's leading edge.
	const micOpen = useSharedValue(dictating ? 1 : 0);
	useEffect(() => {
		micOpen.value = withTiming(dictating ? 1 : 0, { duration: reduceMotion ? 0 : COMPOSER_FADE_MS });
	}, [dictating, micOpen, reduceMotion]);
	useEffect(() => { onExpandedChange?.(expanded); }, [expanded, onExpandedChange]);
	useEffect(() => {
		openness.value = reduceMotion ? withTiming(expanded ? 1 : 0, { duration: 0 }) : withSpring(expanded ? 1 : 0, COMPOSER_SPRING);
	}, [expanded, openness, reduceMotion]);
	// How open the pill is drawn: its spring, or the keyboard's progress when
	// further along, so it opens with the keys rather than once JS hears of the
	// focus. Each worklet reads the shared values itself: Reanimated tracks only
	// the values a worklet reads directly.
	const rideKeyboard = !dictating;
	const shownOpenness = (open: number, keyboardProgress: number, closingWithKeyboard: boolean) => {
		"worklet";
		if (!rideKeyboard) return open;
		const ride = dockRiseOverride ?? keyboardProgress;
		return closingWithKeyboard ? ride : Math.max(open, ride);
	};
	// A tap on the conversation blurs the field and closes the keyboard; the
	// pill, open only for that focus, closes with the keys rather than on a
	// spring that waits for the blur to reach JS. Nothing else may hold it open.
	const { closedByBlur } = useKeyboardMotion();
	const { input: focusedInput } = useReanimatedFocusedInput();
	const held = !dictating && (keyboardSend.holding || hasDraft || attachments.length > 0);
	const heldOpen = useSharedValue(held);
	useEffect(() => { heldOpen.value = held; }, [held, heldOpen]);
	const primaryAction = composerPrimaryAction({ active, hasDraft, hasAttachments: attachments.length > 0 });
	const steerEligible = Boolean(canSteer && hasDraft && attachments.length === 0 && quotes.length === 0);
	const deliveryPresentation = composerDeliveryPresentation({ active, canSteer: Boolean(canSteer && quotes.length === 0), hasDraft, hasAttachments: attachments.length > 0, hasQueued: visibleQueuedMessages.length > 0 });
	const stopped = snapshot.controller.state === "stopped";
	const draftKey = machine ? `ao.chat.draft.${machine}.${sessionId}` : null;
	const draftTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const pickerGate = useRef(createRequestGate()).current;
	const latestQuotes = useRef(quotes);
	latestQuotes.current = quotes;
	const latestText = useRef(text);
	const latestCursor = useRef(cursor);
	latestText.current = text;
	latestCursor.current = cursor;
	useEffect(() => {
		if (!quoteRequest || consumedQuote.current === quoteRequest.id) return;
		consumedQuote.current = quoteRequest.id;
		const next = [...latestQuotes.current, { id: quoteRequest.id, text: quoteRequest.text, source: quoteRequest.source }];
		latestQuotes.current = next;
		setQuotes(next);
		onQuoteHandled?.(quoteRequest.id);
		haptics.select();
		requestAnimationFrame(() => inputRef.current?.focus());
	}, [quoteRequest, onQuoteHandled]);
	useEffect(() => () => pickerGate.invalidate(), [pickerGate]);
	useEffect(() => {
		const queuedIds = new Set(queuedMessages.map((entry) => entry.turnId));
		setHiddenQueuedTurnIds((current) => {
			const next = new Set([...current].filter((id) => queuedIds.has(id)));
			return next.size === current.size ? current : next;
		});
	}, [queuedMessages]);

	useEffect(() => {
		if (!draftKey) return;
		let mounted = true;
		void AsyncStorage.getItem(draftKey)
			.then((value) => {
				if (!mounted) return;
				const draft = decodeResponseDraft(value);
				// Text typed before the read finished wins, and so does its caret.
				if (!latestText.current) setCursor(undefined);
				setText((current) => current || draft.text);
				setQuotes((current) => [...draft.quotes.filter((quote) => !current.some((item) => item.id === quote.id)), ...current]);
			})
			.catch(() => {})
			.finally(() => { if (mounted) setDraftLoaded(true); });
		return () => { mounted = false; };
	}, [draftKey]);
	useEffect(() => {
		if (!draftKey || !draftLoaded) return;
		draftTimer.current = setTimeout(() => {
			const value = encodeResponseDraft(text, quotes);
			void afterDraftWrites(draftKey, () => value ? AsyncStorage.setItem(draftKey, value) : AsyncStorage.removeItem(draftKey)).catch(() => {});
		}, 250);
		return () => { if (draftTimer.current) clearTimeout(draftTimer.current); };
	}, [draftKey, draftLoaded, text, quotes]);
	useEffect(() => {
		if (!completedRetry) return;
		if (draftTimer.current) clearTimeout(draftTimer.current);
		const current = latestText.current;
		const currentQuotes = latestQuotes.current;
		const matches = currentQuotes.length <= 8 && currentQuotes.every((quote) => isChatExcerpt({ ...quote.source, text: quote.text })) && isSentDraft(completedRetry.draftText, responseReplyText(current, currentQuotes), responseExcerpts(currentQuotes));
		if (matches) {
			latestText.current = "";
			setText("");
			latestQuotes.current = [];
			setQuotes([]);
			clearAttachments();
		}
		void (async () => {
			if (draftKey) {
				await afterDraftWrites(draftKey, () => matches ? AsyncStorage.removeItem(draftKey) : AsyncStorage.setItem(draftKey, encodeResponseDraft(current, currentQuotes)));
			}
			await onAcknowledgeSend(completedRetry.id);
		})().catch((cause) => setLocalError(userFacingError(cause)));
	}, [completedRetry, draftKey, onAcknowledgeSend, clearAttachments]);


	// Whether a send can start now. Hardware Return is taken only then; otherwise it
	// stays the field's and inserts a newline, rather than being caught and dropped.
	const canSubmit = !submitting && !pending && !disabled && !(draftKey && !draftLoaded);
	// Claude Code's Ultracode, switched on over the effort slider: each message asks for it.
	const ultracodeOn = useRef<() => boolean>(() => false);
	// Sets the conversation's Ultracode once a switch lands.
	const keepUltracode = useRef<(on: boolean) => void>(() => {});
	// The agent a message goes to: the one running, or a switch picked to run
	// with it, and whether that switch asks for Ultracode.
	const receiving = useRef({ harness: snapshot.harness, switchTo: otherAgents?.pending?.harness, switchUltracode: Boolean(otherAgents?.pending?.ultracode) });
	receiving.current = { harness: snapshot.harness, switchTo: otherAgents?.pending?.harness, switchUltracode: Boolean(otherAgents?.pending?.ultracode) };
	const submit = useCallback(async (intent: ComposerDeliveryIntent = "send", from: "button" | "keyboard" = "button") => {
		if (!canSubmit) return;
		const trimmed = responseReplyText(text, quotes);
		if (!trimmed && attachments.length === 0) return;
		// This send owns these attachments and no others: it can finish after the
		// conversation was left and reopened, and the new composer may have added more.
		const sentKey = attachmentKey;
		const sent = new Set(attachments.map((item) => item.id));
		// Dismissed on the tap, not after the send lands. Waiting for the request
		// tied the keyboard's exit to the network, so it dropped whenever the
		// answer arrived — which read as the keyboard being taken away mid-sentence.
		// Return on a hardware keyboard keeps the field: the next message is typed
		// straight after, as at a desk.
		if (from === "keyboard") keyboardSend.begin();
		else Keyboard.dismiss();
		setLocalError(undefined);
		dismissAttachmentNotice();
		setSubmitting(true);
		if (draftTimer.current) clearTimeout(draftTimer.current);
		try {
			// Submission cancels the debounce; persist before a preflight can reject.
			if (draftKey) {
				await afterDraftWrites(draftKey, () => AsyncStorage.setItem(draftKey, encodeResponseDraft(text, quotes)));
			}
			const excerpts = responseExcerpts(quotes);
			const images = attachments.filter((item): item is Extract<Attachment, { kind: "image" }> => item.kind === "image").map((item) => item.image);
			const resources = attachments.filter((item): item is Extract<Attachment, { kind: "resource" }> => item.kind === "resource").map((item) => item.resource);
			const route = composerDeliveryRoute(intent, steerEligible);
			// With Ultracode's prefix added, the draft as typed goes too: a retry's
			// completion matches the composer against it, exactly.
			// Ultracode is Claude Code's. A send that switches the chat goes to the
			// agent picked, and asks for Ultracode as picked for that switch
			// (desktop's pendingSwitch.ultracode): a switch to another agent never
			// gets a prefix nobody typed. A steer stays with the agent running now.
			const { harness: running, switchTo, switchUltracode } = receiving.current;
			const switching = route === "steer" ? undefined : switchTo;
			const asksUltracode = switching
				? switching === ULTRACODE_HARNESS && switchUltracode && !cloud
				: running === ULTRACODE_HARNESS && ultracodeOn.current();
			const sentText = asksUltracode ? withUltracode(trimmed) : trimmed;
			const typed: [] | [string] = sentText === trimmed ? [] : [trimmed];
			const id = route === "steer"
				? await onSteer(sentText, ...typed)
				: await onSend(sentText, images.length ? images : undefined, resources.length ? resources : undefined, excerpts.length ? excerpts : undefined, ...typed);
			// The switch landed: Ultracode stays as picked for it for the messages
			// after it, off for any agent but Claude Code, as desktop's switched()
			// leaves it.
			if (switching) keepUltracode.current(asksUltracode);
			if (draftKey) {
				const current = latestText.current;
				const currentQuotes = latestQuotes.current;
				const unchanged = current === text && currentQuotes === quotes;
				await afterDraftWrites(draftKey, () => unchanged ? AsyncStorage.removeItem(draftKey) : AsyncStorage.setItem(draftKey, encodeResponseDraft(current, currentQuotes)));
			}
			await onAcknowledgeSend(id);
			if (latestText.current === text && latestQuotes.current === quotes) {
				latestText.current = "";
				setText("");
				latestQuotes.current = [];
				setQuotes([]);
			}
			forgetComposerAttachments(sentKey, sent);
			haptics.success();
		} catch (cause) {
			setLocalError(userFacingError(cause));
			haptics.error();
		} finally { setSubmitting(false); }
	}, [text, quotes, attachments, steerEligible, onSteer, onSend, onAcknowledgeSend, draftKey, canSubmit, keyboardSend.begin, attachmentKey, dismissAttachmentNotice, cloud]);

	// Pasting is a way to attach too, so it follows the same gate as the menu.
	const pasteLayout = useImagePaste(stopped || submitting || attachmentsUnavailable ? null : (assets, overflow) => {
		setLocalError(undefined);
		if (attachComposerImages(attachmentKey, assets, overflow)) haptics.tap();
	});
	const addImage = async () => {
		// Captured now: the pick can outlive this composer, and the stored list may
		// have changed while the picker was open.
		const key = attachmentKey;
		setLocalError(undefined);
		dismissAttachmentNotice();
		try {
			const result = await ImagePicker.launchImageLibraryAsync({ mediaTypes: ["images"], base64: true, quality: 0.82, allowsMultipleSelection: true, selectionLimit: 4 });
			if (result.canceled) return;
			attachComposerImages(key, result.assets);
		} catch (cause) {
			const notice = userFacingError(cause, "Couldn't open your photo library.");
			updateComposerAttachments(key, (current) => ({ items: current.items, notice }));
		}
	};
	const addFile = async () => {
		if (fileUploads) return addUploadFile(fileUploads.maxBytes);
		const key = attachmentKey;
		setLocalError(undefined);
		dismissAttachmentNotice();
		try {
			// Inside the try: a picker that fails says so rather than rejecting unseen.
			const result = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true, type: ["text/*", "application/json", "application/xml", "application/yaml"] });
			if (result.canceled) return;
			const existing = readComposerAttachments(key).items.length;
			const added: Attachment[] = [];
			for (const asset of result.assets) {
				if (existing + added.length >= MAX_ATTACHMENTS) throw new Error(`You can attach up to ${MAX_ATTACHMENTS} items.`);
				if ((asset.size ?? 0) > MAX_EMBEDDED_FILE_BYTES) throw new Error(`${asset.name} is larger than 500 KB. Reference a worktree file with @ instead.`);
				const body = await fetch(asset.uri).then((response) => response.text());
				const bytes = new TextEncoder().encode(body).byteLength;
				if (bytes > MAX_EMBEDDED_FILE_BYTES) throw new Error(`${asset.name} is too large to embed. Reference a worktree file with @ instead.`);
				added.push({ id: `${asset.uri}-${Date.now()}`, kind: "resource", name: asset.name, bytes, resource: { uri: `mobile-attachment://${encodeURIComponent(asset.name)}`, name: asset.name, mimeType: asset.mimeType || "text/plain", text: body } });
			}
			updateComposerAttachments(key, (current) => ({ items: [...current.items, ...added], notice: current.notice }));
		} catch (cause) {
			const notice = userFacingError(cause);
			updateComposerAttachments(key, (current) => ({ items: current.items, notice }));
		}
	};

	// Any file, kept as a local copy and uploaded with the message (see `fileUploads`).
	const addUploadFile = async (maxBytes: number) => {
		const key = attachmentKey;
		setLocalError(undefined);
		dismissAttachmentNotice();
		try {
			// Inside the try: a picker that fails says so rather than rejecting unseen.
			const result = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true, type: "*/*" });
			if (result.canceled) return;
			const existing = readComposerAttachments(key).items.length;
			const added: Attachment[] = [];
			for (const asset of result.assets) {
				if (existing + added.length >= MAX_ATTACHMENTS) throw new Error(`You can attach up to ${MAX_ATTACHMENTS} items.`);
				const bytes = asset.size ?? 0;
				if (bytes > maxBytes) throw new Error(`${asset.name} is larger than ${Math.round(maxBytes / (1 << 20))} MB, the most this session takes.`);
				added.push({ id: `${asset.uri}-${Date.now()}`, kind: "resource", name: asset.name, bytes, resource: { uri: asset.uri, name: asset.name, mimeType: asset.mimeType || "application/octet-stream" } });
			}
			updateComposerAttachments(key, (current) => ({ items: [...current.items, ...added], notice: current.notice }));
		} catch (cause) {
			const notice = userFacingError(cause);
			updateComposerAttachments(key, (current) => ({ items: current.items, notice }));
		}
	};

	// `/` at the start of the message or `@` after a space lists skills or
	// worktree files over the composer. The field keeps focus, so what follows
	// the sigil is the filter; a choice replaces the token, and the caret leaving
	// it (a space, a deleted sigil) closes the list.
	// Only while the field has focus: with the keyboard down a list over the
	// composer would answer a question nobody is asking.
	const suggestion = useMemo(() => focused ? findComposerSuggestion(text, cursor) : undefined, [cursor, focused, text]);
	const suggestionKind = suggestion?.kind;
	const [loadedSkills, setLoadedSkills] = useState<ChatSkill[]>();
	const [loadedFiles, setLoadedFiles] = useState<{ paths: string[]; truncated: boolean }>();
	const [loadingSuggestions, setLoadingSuggestions] = useState<ComposerSuggestion["kind"]>();
	// The loaders change identity whenever their results land; the list should
	// reload when it opens, not every time a load settles.
	const loaders = useRef({ onLoadSkills, onLoadFiles });
	loaders.current = { onLoadSkills, onLoadFiles };
	useEffect(() => {
		if (!suggestionKind) {
			pickerGate.invalidate();
			setLoadingSuggestions(undefined);
			return;
		}
		const request = pickerGate.begin();
		setLoadingSuggestions(suggestionKind);
		const load = suggestionKind === "skills"
			? loaders.current.onLoadSkills().then((next) => { if (pickerGate.isCurrent(request)) setLoadedSkills(next); })
			: loaders.current.onLoadFiles().then((next) => { if (pickerGate.isCurrent(request)) setLoadedFiles(next); });
		// A failed reload drops the earlier result rather than keep it over the
		// current `skills`/`filePaths`, which may be newer.
		void load
			.catch(() => {
				if (!pickerGate.isCurrent(request)) return;
				if (suggestionKind === "skills") setLoadedSkills(undefined);
				else setLoadedFiles(undefined);
			})
			.finally(() => { if (pickerGate.isCurrent(request)) setLoadingSuggestions(undefined); });
	}, [pickerGate, suggestionKind]);
	const suggestionItems = useMemo(() => {
		if (!suggestion) return undefined;
		const catalog: ComposerPickerCatalog = suggestion.kind === "skills"
			? { kind: "skills", skills: loadedSkills ?? skills }
			: { kind: "files", paths: loadedFiles?.paths ?? filePaths };
		const empty = catalog.kind === "skills" ? !catalog.skills.length : !catalog.paths.length;
		if (empty && loadingSuggestions === suggestion.kind) return undefined;
		return rankComposerCatalog(catalog, suggestion.query);
	}, [filePaths, loadedFiles, loadedSkills, loadingSuggestions, skills, suggestion]);
	// Text that only looks like a trigger (`/usr/bin`, an unknown command) gets
	// no list rather than an empty one in the way.
	const showSuggestions = Boolean(suggestion && !(suggestionItems?.length === 0 && suggestion.query.trim()));
	const selectSuggestion = useCallback((value: string) => {
		const trigger = findComposerSuggestion(latestText.current, latestCursor.current);
		if (!trigger) return;
		const next = replaceComposerSuggestion(latestText.current, trigger, value);
		// After the separator, whether inserted or already there: a caret left
		// against the token would still read as the trigger and keep the list open.
		const tokenEnd = next.length - (latestText.current.length - trigger.end);
		const caret = /\s/.test(next[tokenEnd] ?? "") ? tokenEnd + 1 : tokenEnd;
		latestText.current = next;
		latestCursor.current = caret;
		setText(next);
		setCursor(caret);
	}, []);
	const settingsProps = { snapshot, models, options: configOptions ?? NO_OPTIONS, disabled: disabled || stopped || pending || submitting, onSettings, onOption: onConfigOption };
	// Ultracode has no field on the daemon: it is kept per conversation here.
	const effort = useChatEffort({ ...settingsProps, ultracodeKey: machine ? ultracodeKey(machine, sessionId) : null, cloud });
	// Claude's Fast, learned for New task: the model this chat runs offers it,
	// so New task offers the bolt for it; and a task started there with Fast on
	// has it turned on here, once Claude reports it (see learnedFast).
	const fastModelLabel = useMemo(() => (effort.fast ? turnSettingsParts(snapshot, models, configOptions ?? NO_OPTIONS).model : ""), [effort.fast, snapshot, models, configOptions]);
	useEffect(() => {
		if (machine && !cloud && fastModelLabel) void rememberFastModel(machine, snapshot.harness, fastModelLabel);
	}, [machine, cloud, fastModelLabel, snapshot.harness]);
	usePendingFast(machine && !cloud ? fastPendingKey(machine, sessionId) : null, effort.fast, Boolean(configOptions?.length));
	// The agent's settings as it reports them, kept for New task's pickers (chatSettingsTemplate).
	useEffect(() => {
		if (machine && !cloud && configOptions?.length) void rememberChatSettings(machine, snapshot.harness, configOptions);
	}, [machine, cloud, configOptions, snapshot.harness]);
	// Read as the message goes.
	ultracodeOn.current = effort.sendsUltracode;
	keepUltracode.current = effort.keepUltracode;
	const permissions = useChatPermissions(settingsProps);
	// Fast mode and Ultracode over the slider are the running agent's. With a
	// switch to another agent picked, the next message goes to that one, so
	// they are the switch's own, sent with it as desktop's pending switch has
	// them, and never the running agent's: its Fast mode where its model has
	// one, its Ultracode where it goes to Claude Code on a machine of the
	// user's own.
	const pendingPick = otherAgents?.pending;
	const switchFast = useMemo(() => {
		if (!pendingPick || !otherAgents || !switchOffersFast(pendingPick, otherAgents.fast)) return undefined;
		const pick = otherAgents.onPick;
		return { on: Boolean(pendingPick.fastMode), toggle: () => pick({ ...pendingPick, fastMode: !pendingPick.fastMode || undefined }) };
	}, [pendingPick, otherAgents]);
	const switchUltracode = useMemo(() => {
		if (!pendingPick || !otherAgents || pendingPick.harness !== ULTRACODE_HARNESS || cloud) return undefined;
		const pick = otherAgents.onPick;
		return { on: Boolean(pendingPick.ultracode), toggle: () => pick({ ...pendingPick, ultracode: !pendingPick.ultracode || undefined }) };
	}, [pendingPick, otherAgents, cloud]);
	const effortFast = pendingPick ? switchFast : effort.fast;
	const effortUltracode = pendingPick ? switchUltracode : effort.ultracode;
	// The gauge shows the switches that are on, and opens the slider for them
	// even on a model with no levels.
	const effortToggles = effortGaugeToggles(effortFast, effortUltracode);
	// Fast mode is the effort slider's bolt wherever it is offered (the gauge
	// shows for it with no levels too), so the wheels leave it out.
	const modelWheel = useChatModelWheel({ ...settingsProps, otherAgents, fastOnEffort: Boolean(effortFast) });
	const overlays = useMemo(() => [effort.slider, permissions.slider, modelWheel.slider], [effort.slider, permissions.slider, modelWheel.slider]);
	// Typing is a tap outside the slider and the picker that the keyboard, not
	// the app, hears.
	const closeEffort = effort.close;
	const closePermissions = permissions.close;
	useEffect(() => { closeEffort(); closePermissions(); }, [text, closeEffort, closePermissions]);
	const placeholder = stopped ? "Agent is stopped" : startingAgentName ? `${startingAgentName} is starting…` : deliveryPresentation.placeholder;
	const activeReviewPR = reviewPromptAvailable ? reviewPR : undefined;
	const sendBlocked = Boolean(disabled || stopped || pending || submitting || (draftKey && !draftLoaded));
	const nothingToSend = !hasDraft && attachments.length === 0;
	// A full touch target around a compact tinted disc, so the controls row keeps
	// its weight while the button meets the platform minimum.
	const steerAction = deliveryPresentation.showSteerAction ? <Pressable
		accessibilityRole="button"
		accessibilityLabel="Steer this turn now"
		accessibilityState={{ disabled: disabled || submitting || pending }}
		disabled={disabled || submitting || pending}
		android_ripple={iconFeedback.android_ripple}
		onPress={() => void submit("steer")}
		style={({ pressed }) => [styles.steerAction, pressed && iconFeedback.pressed]}
	>
		<View style={styles.steerDisc}><AppSymbol name={steer} size={iconSize.sm} color={t.accent} /></View>
	</Pressable> : null;
	const attachMenu = <ChatAttachmentMenu disabled={stopped || submitting || Boolean(attachmentsUnavailable)} canAttachFile={Boolean(canEmbedFiles || fileUploads)} onChoosePhoto={() => void addImage()} onChooseFile={() => void addFile()} />;
	// A denied mic still dictates from here: its tap opens Settings, where the
	// microphone can be turned back on.
	const canDictate = voice.state !== "unavailable";
	const micDenied = voice.state === "denied";
	// One round button that follows the prompt, as on the new-task sheet: it
	// stops the mic while it is open, stops the turn while one runs and nothing
	// is drafted, sends once there is something to send, and otherwise dictates.
	// Dictation here is hands-free (tap, speak, tap again), and it drops the
	// keyboard: the card stays open while the mic is.
	const primary = listening ? "stopDictation" : transcribing ? "transcribing" : primaryAction === "stop" ? "stopTurn" : nothingToSend && canDictate ? "dictate" : "send";
	const roundButton = primary === "stopTurn"
		? <Pressable accessibilityRole="button" accessibilityLabel="Stop turn" accessibilityState={{ busy: interrupting, disabled: disabled || interrupting }} disabled={disabled || interrupting} android_ripple={buttonFeedback.android_ripple} onPress={() => { haptics.tap(); void onInterrupt(); }} style={({ pressed }) => [styles.roundTarget, pressed && buttonFeedback.pressed, (disabled || interrupting) && styles.inactive]}><View style={[styles.roundDisc, styles.roundStop]}>{interrupting ? <ActivityIndicator size="small" color={t.textPrimary} /> : <AppSymbol name="stop" size={iconSize.xs} weight="bold" color={t.textPrimary} />}</View></Pressable>
		: primary === "send"
			? <Pressable accessibilityRole="button" accessibilityLabel={active ? "Queue message" : "Send message"} accessibilityState={{ disabled: sendBlocked }} disabled={sendBlocked || nothingToSend} android_ripple={sendFeedback.android_ripple} onPress={() => void submit("send")} style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed, (sendBlocked || nothingToSend) && styles.inactive]}><View style={[styles.roundDisc, styles.roundAccent]}>{pending || submitting ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.sm} weight="bold" color={t.onAccent} />}</View></Pressable>
			: <Pressable
				accessibilityRole="button"
				accessibilityLabel={primary === "stopDictation" ? "Stop dictating" : primary === "transcribing" ? "Transcribing" : micDenied ? "Turn on microphone access in Settings" : "Dictate a message"}
				accessibilityState={{ disabled: stopped || submitting || transcribing, busy: transcribing }}
				disabled={stopped || submitting || transcribing}
				android_ripple={sendFeedback.android_ripple}
				onPress={() => { if (primary === "dictate") Keyboard.dismiss(); voice.toggleLatched(); }}
				style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed, (stopped || submitting) && styles.inactive]}
			>
				<View style={[styles.roundDisc, primary === "stopDictation" ? styles.dictating : styles.roundAccent]}>
					{transcribing ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name={primary === "stopDictation" ? "stop" : micDenied ? "micOff" : dictateGlyph} size={iconSize.sm} weight="bold" color={primary === "stopDictation" ? t.onDestructive : t.onAccent} />}
				</View>
			</Pressable>;
	// The corner controls stay where they are; the pill grows up around them. The
	// field starts between them and is let out to the pill's full width, and the
	// controls row opens under it, its settings rising into place.
	const fieldInsets = useAnimatedStyle(() => {
		const open = shownOpenness(openness.value, keyboard.progress.value, closedByBlur.value && focusedInput.value === null && !heldOpen.value);
		return {
			paddingLeft: interpolate(micOpen.value, [0, 1], [interpolate(open, [0, 1], [CORNER_CONTROL, OPEN_FIELD_INSET]), DICTATION_INSET]),
			paddingRight: interpolate(open, [0, 1], [CORNER_CONTROL, OPEN_FIELD_INSET]),
		};
	});
	const controlsHeight = (open: number) => {
		"worklet";
		return interpolate(open, [0, 1], [0, touchTarget], "clamp");
	};
	// The pill's height without its controls row: down to the field row's foot
	// and the pill's own bottom padding, from the field row's layout.
	const pillBase = useSharedValue(0);
	// The field row's layout, read where both go: `pillBase` here, and
	// `fieldGrowth` for the conversation over the composer.
	const fieldRow = useAnimatedRef<Animated.View>();
	const fieldMinHeight = fieldSize.field.minHeight;
	const takeFieldRow = (y: number, height: number) => {
		"worklet";
		const base = y + height + space.xs;
		if (base !== pillBase.value) pillBase.value = base;
		// One empty line sits at the pill's top padding, the field's minimum tall.
		const growth = Math.max(0, y + height - space.xs - fieldMinHeight);
		if (fieldGrowth && growth !== fieldGrowth.value) fieldGrowth.value = growth;
	};
	// For a moment after the field changes, its row is read on the UI thread
	// each frame, so the conversation moves with a new line rather than a line
	// late. Only briefly: a callback left running kept the app drawing idle.
	const watchFieldRow = useFrameCallback(() => {
		"worklet";
		// Not while the keyboard moves: nothing is typed then.
		const progress = keyboard.progress.value;
		if (progress > 0 && progress < 1) return;
		// Nor while a request card stands in for the row. The unmounted row reads
		// null here, and measure() passes that to the native side (it only checks
		// for -1), which throws on the UI thread and takes the whole app down.
		if (!fieldRow()) return;
		const layout = measure(fieldRow);
		if (layout) takeFieldRow(layout.y, layout.height);
	}, false);
	const fieldWatchEnd = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const watchFieldBriefly = useCallback(() => {
		if (!fieldGrowth) return;
		watchFieldRow.setActive(true);
		clearTimeout(fieldWatchEnd.current);
		fieldWatchEnd.current = setTimeout(() => watchFieldRow.setActive(false), FIELD_WATCH_MS);
	}, [fieldGrowth, watchFieldRow]);
	useEffect(() => () => {
		clearTimeout(fieldWatchEnd.current);
		watchFieldRow.setActive(false);
	}, [watchFieldRow]);
	// Field changes other than a keystroke.
	useEffect(() => { watchFieldBriefly(); }, [text, quotes.length, trayItems.length, expanded, dictating, watchFieldBriefly]);
	// How much shorter the pill is than open. The closed pill stands on a floor
	// (COMPOSER_HEIGHT) the controls row fills first.
	const pillShown = !requestCard;
	// A request card unmounts the field row, which then no longer reports its
	// growth; the row's layout restores it when the card goes.
	useEffect(() => { if (!pillShown && fieldGrowth && fieldGrowth.value !== 0) fieldGrowth.value = 0; }, [pillShown, fieldGrowth]);
	useAnimatedReaction(() => {
		const base = pillBase.value;
		const floor = Math.max(COMPOSER_HEIGHT, base);
		const drawn = Math.max(COMPOSER_HEIGHT, base + controlsHeight(shownOpenness(openness.value, keyboard.progress.value, closedByBlur.value && focusedInput.value === null && !heldOpen.value))) - floor;
		const open = Math.max(COMPOSER_HEIGHT, base + touchTarget) - floor;
		return open - drawn;
	}, (deficit) => {
		const next = pillShown ? deficit : 0;
		if (openDeficit && openDeficit.value !== next) openDeficit.value = next;
	}, [openDeficit, pillShown, rideKeyboard, dockRiseOverride]);
	const controlsOpening = useAnimatedStyle(() => {
		const open = shownOpenness(openness.value, keyboard.progress.value, closedByBlur.value && focusedInput.value === null && !heldOpen.value);
		return {
			height: controlsHeight(open),
			opacity: interpolate(open, [0.35, 1], [0, 1], "clamp"),
			transform: [{ translateY: interpolate(open, [0, 1], [space.sm, 0], "clamp") }],
		};
	});
	// The session's state as chips over the composer, open or not: the account's
	// quota once that matters, its PR, what it changed, the device it holds, and
	// how full the context is once that matters.
	const chips = activeReviewPR || changes || device || contextMeter || quotaChip ? <Animated.View entering={FadeIn.duration(COMPOSER_FADE_MS)} exiting={FadeOut.duration(COMPOSER_FADE_MS)}>
		<ScrollView key={fieldSize.fontScale} horizontal showsHorizontalScrollIndicator={false} keyboardShouldPersistTaps="handled" contentContainerStyle={styles.chips}>
			{/* First: the row scrolls with no indicator, and this is the only quota
			    warning, so it must not start off screen behind the PR and changes chips. */}
			{quotaChip ? <ComposerChip accessibilityLabel={quotaChip.accessibilityLabel} accessibilityHint="Opens context and usage" onPress={onOpenUsage}>
				<AppSymbol name="warning" size={iconSize.xs} color={quotaChip.severity === "critical" ? t.red : t.amber} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.contextText, styles.tabular, { color: quotaChip.severity === "critical" ? t.red : t.amber }]}>{quotaChip.label}</Text>
			</ComposerChip> : null}
			{activeReviewPR ? <ComposerChip accessibilityLabel={prChipAccessibilityLabel(activeReviewPR, reviewPRSummary)} accessibilityHint="Opens the pull request review" onPress={onOpenReview}>
				<PRChipContent pr={activeReviewPR} summary={reviewPRSummary} />
			</ComposerChip> : null}
			{changes ? <ComposerChip accessibilityLabel={changesAccessibilityLabel(changes)} accessibilityHint="Opens the changed files" onPress={changes.onOpen}>
				<AppSymbol name="diff" size={iconSize.xs} color={t.textSecondary} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.chipText}>{changes.files}</Text>
				{changes.additions ? <Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.chipCount, { color: t.green }]}>+{compactCount(changes.additions)}</Text> : null}
				{changes.deletions ? <Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.chipCount, { color: t.red }]}>−{compactCount(changes.deletions)}</Text> : null}
			</ComposerChip> : null}
			{device ? <ComposerChip accessibilityLabel={`Open ${device.name}`} accessibilityHint="Opens the device screen" onPress={device.onOpen}>
				<AppSymbol name="phone" size={iconSize.xs} color={t.textSecondary} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.chipText}>{device.name}</Text>
			</ComposerChip> : null}
			{contextMeter ? <View accessible accessibilityRole="progressbar" accessibilityLabel="Context window" accessibilityValue={contextMeter.accessibilityValue} style={styles.chip}>
				<ComposerGlass radius={CHIP_HEIGHT / 2} />
				<View style={styles.contextTrack}>
					<View style={[styles.contextFill, { width: `${contextMeter.fillPercent}%`, backgroundColor: contextMeter.severity === "critical" ? t.red : t.amber }]} />
				</View>
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.contextText, { color: contextMeter.severity === "critical" ? t.red : t.amber }]}>{contextMeter.label}</Text>
			</View> : null}
		</ScrollView>
	</Animated.View> : null;
	return (
		// The dock holds its resting inset at all times and rides the keyboard's own
		// progress to close the difference, so its distance to the keyboard is
		// `KEYBOARD_DOCK_GAP` at every frame of the animation rather than only once
		// the keyboard has finished moving. It floats over the conversation with no
		// fill of its own: the chips and the composer carry their own glass.
		<Animated.View style={[styles.dock, styles.floatingDock, { paddingBottom: restingInset }, dockRise]}>
			<DockFade />
			{chips}
			{/* Composer-local only. Conversation and action failures are banners above
			    the timeline; echoing them here showed one failure twice. */}
			{localError || attachmentNotice || voice.error ? <Text accessibilityRole="alert" style={styles.error}>{localError || attachmentNotice || voice.error}</Text> : null}
			{visibleQueuedMessages.length ? <View accessibilityRole="list" style={styles.queueDock}>
				{visibleQueuedMessages.map((entry, index) => {
					const promoting = promotingQueuedTurnId === entry.turnId;
					const cancelling = cancellingQueuedTurnId === entry.turnId;
					const queueActionPending = Boolean(promotingQueuedTurnId || cancellingQueuedTurnId);
					return <View key={entry.turnId} style={[styles.queueRow, index > 0 && styles.queueRowDivider]}>
						<AppSymbol name={queued} size={iconSize.sm} color={t.textTertiary} />
						<Text numberOfLines={1} style={styles.queueText}>{entry.message.text}</Text>
						{canSteer && onPromoteQueuedTurn ? <Pressable
							accessibilityRole="button"
							accessibilityLabel={`Steer queued message: ${entry.message.text}`}
							accessibilityState={{ busy: promoting, disabled: queueActionPending }}
							disabled={queueActionPending}
							android_ripple={iconFeedback.android_ripple}
							onPress={() => {
								setPromotingQueuedTurnId(entry.turnId);
								setHiddenQueuedTurnIds((current) => new Set(current).add(entry.turnId));
								void onPromoteQueuedTurn(entry.turnId)
									.then(() => haptics.success())
									.catch(() => {
										setHiddenQueuedTurnIds((current) => {
											const next = new Set(current);
											next.delete(entry.turnId);
											return next;
										});
										haptics.error();
									})
									.finally(() => setPromotingQueuedTurnId(undefined));
							}}
							style={({ pressed }) => [styles.queueSteer, pressed && iconFeedback.pressed]}
						>
							{promoting ? <ActivityIndicator size="small" color={t.accent} /> : <AppSymbol name={steer} size={iconSize.sm} color={t.accent} />}
						</Pressable> : null}
						{onCancelQueuedTurn ? <Pressable
							accessibilityRole="button"
							accessibilityLabel={`Delete queued message: ${entry.message.text}`}
							accessibilityState={{ busy: cancelling, disabled: queueActionPending }}
							disabled={queueActionPending}
							android_ripple={iconFeedback.android_ripple}
							onPress={() => {
								setCancellingQueuedTurnId(entry.turnId);
								void onCancelQueuedTurn(entry.turnId)
									.then(() => haptics.success())
									.catch(() => haptics.error())
									.finally(() => setCancellingQueuedTurnId(undefined));
							}}
							style={({ pressed }) => [styles.queueDelete, pressed && iconFeedback.pressed]}
						>
							{cancelling ? <ActivityIndicator size="small" color={t.textTertiary} /> : <AppSymbol name="close" size={iconSize.sm} color={t.textTertiary} />}
						</Pressable> : null}
					</View>;
				})}
			</View> : null}
			{/* The way back to a request pushed aside to type instead: what it is,
			    and a button that brings it back. Screen readers get one stop that
			    names the request, since the button alone would say only "Answer":
			    VoiceOver's double-tap arrives as an accessibility tap, TalkBack's as
			    `activate`. */}
			{request && !requestCard ? <View
				accessible
				accessibilityRole="button"
				accessibilityLabel={`${request.title}. Answer it`}
				accessibilityActions={[{ name: "activate", label: "Answer" }]}
				onAccessibilityAction={(event) => { if (event.nativeEvent.actionName === "activate") onRestoreRequest(); }}
				onAccessibilityTap={onRestoreRequest}
				style={styles.restore}
			>
				<AppSymbol name={request.kind === "approval" ? approvalGlyph : "chat"} size={iconSize.xs} color={t.amber} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.restoreText}>{request.title}</Text>
				<View importantForAccessibility="no-hide-descendants">
					<Button title="Answer" variant="secondary" onPress={onRestoreRequest} />
				</View>
			</View> : null}
			{!requestCard && suggestion && showSuggestions ? <ComposerSuggestionPanel
				kind={suggestion.kind}
				items={suggestionItems}
				truncated={suggestion.kind === "files" ? (loadedFiles?.truncated ?? filePathsTruncated) : undefined}
				onSelect={selectSuggestion}
			/> : null}
			{/* The field spans the pill between its two controls, so a touch on the
			    pill focuses it and the pill opens. */}
			{requestCard ?? <View style={[styles.composer, stopped && styles.inactive]}>
				<ComposerGlass radius={COMPOSER_RADIUS} />
				{quotes.length ? <ScrollView style={styles.quotes} keyboardShouldPersistTaps="handled">
					{quotes.map((quote) => <View key={quote.id} style={styles.quote}>
						<Text numberOfLines={2} style={styles.quoteText} accessibilityLabel={`Quoted response: ${quote.text}`}>{quote.text}</Text>
						<Pressable accessibilityRole="button" accessibilityLabel={`Remove quote: ${quote.text}`} android_ripple={iconFeedback.android_ripple} onPress={() => {
							const next = latestQuotes.current.filter((item) => item.id !== quote.id);
							latestQuotes.current = next;
							setQuotes(next);
						}} style={({ pressed }) => [styles.removeQuote, pressed && iconFeedback.pressed]}><AppSymbol name="close" size={iconSize.md} color={t.textSecondary} /></Pressable>
					</View>)}
				</ScrollView> : null}
				{/* What goes with the message, inside the pill over the field. */}
				<ComposerAttachmentTray items={trayItems} onRemove={(id) => removeComposerAttachment(attachmentKey, id)} />
				{/* The field keeps one place in the tree whatever the pill's shape, so it
				    keeps its focus as the pill opens around it. While the mic is open
				    the dictation takes its place, and the field waits hidden with the
				    draft the transcript will be added to. */}
				<Animated.View ref={fieldRow} style={[styles.composerRow, fieldSize.row, fieldInsets]} onLayout={openDeficit || fieldGrowth ? (event) => {
					const { y, height } = event.nativeEvent.layout;
					takeFieldRow(y, height);
				} : undefined}>
					{dictating ? <DictationField state={voice.state} partial={voice.partial} subscribeLevel={voice.subscribeLevel} style={styles.dictation} waveformOnly /> : null}
					{/* Return on a hardware keyboard (iPad); the wrapper holds the field's
					    place in the row, so it hides with the field while dictating. */}
					<HardwareReturn style={[styles.inputSlot, dictating && styles.hiddenField]} enabled={!sendBlocked} onReturn={(modifiers) => { void submit(hardwareReturnIntent(modifiers), "keyboard"); }}>
						<ComposerField
							ref={inputRef}
							// Its caret and handles step aside for the slider and the picker.
							overlays={overlays}
							onLayout={pasteLayout}
							accessibilityLabel="Message the agent"
							editable={!stopped && !submitting}
							value={text}
							onChangeText={(value) => { watchFieldBriefly(); latestText.current = value; setText(value); }}
							onSelectionChange={(event) => setCursor(event.nativeEvent.selection.start)}
							onFocus={() => { setFocused(true); keyboardSend.focused(); }}
							onBlur={() => setFocused(false)}
							placeholder={placeholder}
							placeholderTextColor={t.textFaint}
							maxFontSizeMultiplier={COMPOSER_FONT_SCALE_CAP}
							// No height of its own: a multiline field grows with its text
							// between the rule's min and max and then scrolls. Setting the
							// height from the content-size event lagged a line behind, so
							// each new line first scrolled the field and cut off its top line.
							style={[styles.input, fieldSize.field, dictating && styles.hiddenField]}
							multiline
							maxLength={40_000}
						/>
					</HardwareReturn>
				</Animated.View>
				{/* Mounted closed too, at no height, so the settings are already
				    drawn when the row opens rather than appearing partway through. */}
				<Animated.View pointerEvents={expanded ? "box-none" : "none"} style={[styles.controlsRow, { paddingLeft: CORNER_CONTROL, paddingRight: CORNER_CONTROL }, controlsOpening]}>
					<View style={styles.settingsSlot}>
						{/* With no agent or model to roll, the wheels have nothing to show:
						    the capsule keeps its menu, so the provider's settings stay reachable. */}
						<ChatTurnSettingsControl {...settingsProps} otherAgents={otherAgents} effort={{ choices: effort.choices, slider: effort.slider, toggles: effortToggles }} permissions={{ choices: permissions.choices, slider: permissions.slider }} modelWheel={modelWheel.agents.length ? { slider: modelWheel.slider } : undefined} />
					</View>
					{steerAction}
				</Animated.View>
				{/* The corner controls, pinned to the pill's bottom corners in both
				    shapes. The round button changes face in place, as the new-task
				    sheet's does. */}
				{dictating ? null : <View style={[styles.corner, styles.cornerLeading]}>{attachMenu}</View>}
				<View style={[styles.corner, styles.cornerTrailing]}>
					{roundButton}
				</View>
			</View>}
			{/* The effort slider and the permission picker open over the
			    keyboard, in a window of their own, as ChatGPT's do (see
			    ComposerOverlay's overKeyboard). */}
			{requestCard ? null : <>
				<EffortSliderHost slider={effort.slider} choices={effort.choices} busy={effort.busy} onChoose={effort.choose} onRelease={effort.commit} onClose={effort.commit} fast={effortFast} ultracode={effortUltracode} overKeyboard />
				<PermissionPickerHost slider={permissions.slider} choices={permissions.choices} busy={permissions.busy} onChoose={permissions.choose} />
				<ModelWheelHost
					slider={modelWheel.slider}
					busy={modelWheel.busy}
					agents={modelWheel.agents}
					chosenAgent={modelWheel.chosenAgent}
					chosenModel={modelWheel.chosenModel}
					runningAgent={modelWheel.runningAgent}
					runningModel={modelWheel.runningModel}
					settings={modelWheel.settings}
					agentsLocked={modelWheel.agentsLocked}
					choose={modelWheel.choose}
					toggleSetting={modelWheel.toggleSetting}
				/>
			</>}
		</Animated.View>
	);
}

/**
 * The conversation fading out behind the dock, as a system bar's scroll edge
 * does: clear above it, nearly the page's colour by the time it reaches the
 * composer, so text running under the chips and the pill never competes with
 * them. Never fully solid: the conversation stays faintly visible below the
 * pill, behind the system navigation, so the composer floats over the page
 * rather than sitting on a strip of its own.
 *
 * The gradient is a strip of fixed height at the dock's top, and the page's
 * colour below it is a plain view that follows the dock's layout. A gradient
 * sized to the whole dock stayed at its old size while the pill sprang open,
 * and left the conversation showing under the opened pill.
 */
function DockFade() {
	const color = useTheme().bgBase;
	return <View pointerEvents="none" style={dockFadeStyle}>
		<View style={{ height: DOCK_FADE_HEIGHT }}>
			<Svg width="100%" height="100%" preserveAspectRatio="none">
				<Defs>
					<LinearGradient id="dockFade" x1="0" y1="0" x2="0" y2="1">
						<Stop offset="0" stopColor={color} stopOpacity={0} />
						<Stop offset="0.5" stopColor={color} stopOpacity={0.7} />
						<Stop offset="1" stopColor={color} stopOpacity={DOCK_FADE_FLOOR} />
					</LinearGradient>
				</Defs>
				<Rect x="0" y="0" width="100%" height="100%" fill="url(#dockFade)" />
			</Svg>
		</View>
		<View style={{ flex: 1, backgroundColor: color, opacity: DOCK_FADE_FLOOR }} />
	</View>;
}

const dockFadeStyle = { position: "absolute", top: -DOCK_FADE_RISE, left: 0, right: 0, bottom: 0 } as const;

/**
 * One of the collapsed composer's chips: a compact glass capsule (an elevated
 * one on Android and before iOS 26) that opens what it names.
 */
function ComposerChip({ accessibilityLabel, accessibilityHint, onPress, children }: { accessibilityLabel: string; accessibilityHint: string; onPress?(): void; children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button");
	return <Pressable
		accessibilityRole="button"
		accessibilityLabel={accessibilityLabel}
		accessibilityHint={accessibilityHint}
		// The chip keeps its compact capsule; the slop makes the target a full touch target tall.
		hitSlop={{ top: CHIP_SLOP, bottom: CHIP_SLOP }}
		android_ripple={feedback.android_ripple}
		disabled={!onPress}
		onPress={onPress}
		style={({ pressed }) => [styles.chip, pressed && feedback.pressed]}
	>
		<ComposerGlass radius={CHIP_HEIGHT / 2} />
		{children}
	</Pressable>;
}

const NO_OPTIONS: ChatConfigOption[] = [];

const makeStyles = (t: Theme) => StyleSheet.create({
	...composerStyleRules(t),
	// No fill: the conversation scrolls on behind the dock, under the chips' and
	// the composer's own glass.
	floatingDock: { backgroundColor: "transparent" },
	// Attach at one bottom corner, the round button at the other, a touch target
	// tall: the field's row when the pill is closed, the controls row when open.
	corner: { position: "absolute", bottom: space.xs, height: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xxs },
	cornerLeading: { left: space.xs },
	cornerTrailing: { right: space.xs },
	// The card's controls: attach, the turn settings, then steer, dictation and the control that commits. The settings label is the
	// only one that can be long, so it is the one that yields: `flex: 1` with
	// `minWidth: 0` to allow the squeeze, and `overflow: "hidden"` so a label that
	// does not truncate cleanly cannot paint over its neighbours. Both are at
	// least a touch target tall, so the trigger's target is never clipped.
	controlsRow: { width: "100%", flexDirection: "row", alignItems: "center", gap: space.xxs, overflow: "hidden" },
	settingsSlot: { flex: 1, minWidth: 0, minHeight: touchTarget, alignItems: "flex-start", justifyContent: "center", overflow: "hidden" },
	chips: { gap: space.xs, paddingVertical: CHIP_SLOP },
	// A capsule; the glass fills it on iOS 26, so the fill drops out there.
	chip: { height: CHIP_HEIGHT, flexDirection: "row", alignItems: "center", gap: space.xxs, paddingHorizontal: space.md, borderRadius: radius.pill, borderCurve: "continuous", overflow: "hidden", backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated, borderWidth: composerGlassSupported ? 0 : rowDividerWidth, borderColor: t.borderDefault },
	// Never shrunk: the chips scroll sideways, so a count is shown whole rather than cut to "+..".
	chipText: { fontFamily: "DMSans_600SemiBold", flexShrink: 0, color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600" },
	chipCount: { fontFamily: t.fontMono, flexShrink: 0, fontSize: type.footnote.fontSize },
	quotes: { maxHeight: 120, flexGrow: 0, marginTop: space.xxs, marginHorizontal: space.sm },
	quote: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.sm },
	quoteText: { ...type.subheadline, flex: 1, minWidth: 0, paddingVertical: space.xs, color: t.textSecondary },
	removeQuote: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	// The composer, or a control in it, that cannot act right now.
	inactive: { opacity: stateOpacity.disabled },
	// The round button: a touch target holding a filled disc the plus's size,
	// its glyphs the new-task sheet's own, bold (arrow.up, mic.fill, stop.fill),
	// the accent to send or dictate, red while the mic is open, and the stop
	// control's quiet fill while it stops the turn.
	roundTarget: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	// Unclipped: the faces swap in place, and Android skips drawing a clipping
	// view's child that resized in the same update as the view's own fill, which
	// left the dictate glyph blank after the stop glyph when a turn ended.
	roundDisc: { width: COMPOSER_DISC, height: COMPOSER_DISC, borderRadius: radius.pill, alignItems: "center", justifyContent: "center" },
	roundAccent: { backgroundColor: t.accent },
	roundStop: { backgroundColor: t.bgSubtle, borderWidth: rowDividerWidth, borderColor: t.borderDefault },
	dictating: { backgroundColor: t.red },
	// The way back to a request the user pushed aside to type instead.
	restore: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingVertical: space.xxs, paddingLeft: space.lg, paddingRight: space.xxs, borderRadius: radius.pill, borderCurve: "continuous", backgroundColor: t.tintAmber },
	restoreText: { fontFamily: "DMSans_600SemiBold", flex: 1, minWidth: 0, color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	// The context meter's chip: the bar carries the reading, the label names it.
	contextTrack: { width: 34, height: 4, borderRadius: radius.pill, overflow: "hidden", backgroundColor: t.bgSubtle },
	contextFill: { height: 4, borderRadius: radius.pill },
	contextText: { fontFamily: "DMSans_600SemiBold", fontSize: type.footnote.fontSize, fontWeight: "600" },
	tabular: { fontVariant: ["tabular-nums"] },
	// As tall as the row; it yields before the controls do.
	steerAction: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	dictation: { minHeight: COMPOSER_FIELD_HEIGHT },
	// Out of the layout but still mounted, with its draft and its place.
	hiddenField: { display: "none" },
	steerDisc: { width: CHIP_HEIGHT, height: CHIP_HEIGHT, alignItems: "center", justifyContent: "center", borderRadius: radius.pill, backgroundColor: t.accentTint },
	queueDock: { overflow: "hidden", backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderDefault, borderRadius: radius.lg, borderCurve: "continuous" },
	queueRow: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.md, paddingRight: space.xxs },
	queueRowDivider: { borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
	queueText: { fontFamily: "DMSans_500Medium", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	queueSteer: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	queueDelete: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});

function changesAccessibilityLabel(changes: { files: number; additions: number; deletions: number }): string {
	const files = `${changes.files} ${changes.files === 1 ? "file" : "files"} changed`;
	return `${files}, ${changes.additions} ${changes.additions === 1 ? "line" : "lines"} added, ${changes.deletions} removed`;
}
