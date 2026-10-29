import AsyncStorage from "@react-native-async-storage/async-storage";
import { machineIdentity, type ServerConfig } from "../config";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, Keyboard, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import Svg, { Defs, LinearGradient, Rect, Stop } from "react-native-svg";
import Animated, { FadeIn, FadeOut, interpolate, useAnimatedStyle, useSharedValue, withSpring, withTiming } from "react-native-reanimated";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { fontScaleCap, iconSize, radius, space, stateOpacity, touchTarget, type } from "../tokens";
import { Button } from "../ui";
import { useReducedMotion } from "../useReducedMotion";
import { KEYBOARD_DOCK_GAP } from "../session/keyboardInset";
import { useVoiceInput } from "../voice/useVoiceInput";
import { activeTurn, type ChatConfigOption, type ChatExcerpt, type ChatImage, type ChatModel, type ChatResource, type ChatSkill, type ConversationSnapshot, type TurnSettings } from "./types";
import {
	composerSuggestionKey,
	findComposerSuggestion,
	replaceComposerSuggestion,
	type ComposerSuggestion,
} from "./composerSuggestions";
import { chatSheetRoute } from "./chatSheetRegistry";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { ComposerAttachment } from "./ComposerAttachment";
import { ComposerGlass, composerGlassSupported } from "./composer-glass";
import { COMPOSER_DISC, COMPOSER_FIELD_HEIGHT, COMPOSER_FIELD_MAX_HEIGHT, COMPOSER_RADIUS, composerStyleRules } from "./composerStyles";
import { ChatTurnSettingsControl } from "./ChatTurnSettingsControl";
import { compactCount } from "../files/model";
import { composerDeliveryPresentation, composerDeliveryRoute, composerPrimaryAction, type ComposerDeliveryIntent } from "./composerDeliveryModel";
import { contextMeterModel } from "./contextMeter";
import { RequestCard } from "./RequestCard";
import type { RequestDockModel } from "./requestDockModel";
import { createRequestGate } from "./requestGate";
import { queuedConversationMessages } from "./timelineModel";
import { userFacingError } from "../connectionError";
import type { DashboardPR, SessionPRSummary } from "../api";
import { PRChipContent, prChipAccessibilityLabel } from "./PRChip";
import { decodeResponseDraft, encodeResponseDraft, responseReplyText, responseExcerpts, excerptDraftText, isChatExcerpt, type ResponseQuote, type ResponseQuoteRequest } from "./responseQuotes";
import { afterDraftWrites } from "./pendingSend";

type Attachment =
	| { id: string; kind: "image"; name: string; bytes: number; image: ChatImage }
	| { id: string; kind: "resource"; name: string; bytes: number; resource: ChatResource };

const MAX_EMBEDDED_FILE_BYTES = 500_000;

const MAX_ATTACHMENTS = 8;
const MAX_IMAGE_BYTES = 10 * 1024 * 1024;
const MAX_IMAGE_BYTES_TOTAL = 25 * 1024 * 1024;
const SUPPORTED_IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/bmp"]);

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
/** The collapsed composer's chips are this tall; their slop makes up the rest of a touch target. */
const CHIP_HEIGHT = space.xxxl;
const CHIP_SLOP = (touchTarget - CHIP_HEIGHT) / 2;
/** The chips and the card's controls fade as the composer opens and closes. */
const COMPOSER_FADE_MS = 160;
/** How far above the dock the conversation starts fading out behind it. */
const DOCK_FADE_RISE = space.xxl;
/** The fade's gradient: from that height down past the chips, solid below. */
const DOCK_FADE_HEIGHT = DOCK_FADE_RISE + CHIP_HEIGHT;

// The composer's one-off glyphs.
/** Redirect the running turn now. */
const steer = { ios: "arrow.turn.up.right", android: "turn_right" } as const;
/** A message waiting its turn in the queue. */
const queued = { ios: "arrow.turn.down.right", android: "subdirectory_arrow_right" } as const;
/** Sent once the running turn ends. */
const sentLater = { ios: "clock", android: "schedule" } as const;
const approvalGlyph = { ios: "shield", android: "shield" } as const;
/** Dictate, filled as on the new-task sheet's round button. */
const dictateGlyph = { ios: "mic.fill", android: "mic" } as const;

export function ChatComposer({
	quoteRequest,
	onQuoteHandled,
	reviewPR,
	reviewPRSummary,
	onOpenReview,
	onExpandedChange,
	sessionId,
	config,
	changes,
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
	restingInset,
	dockRiseOverride,
	quotaActive,
	request,
	requestDismissed,
	onRequestDecide,
	onRequestResolveInput,
	onShowRequest,
	onDismissRequest,
	onRestoreRequest,
	openingTurnId,
	startingAgentName,
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
	/** What this session changed, from git. Present only when it changed something. */
	changes?: { files: number; additions: number; deletions: number; onOpen(): void };
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
	onSend(text: string, attachments?: ChatImage[], resources?: ChatResource[], excerpts?: ChatExcerpt[]): Promise<string>;
	onAcknowledgeSend(id: string): Promise<void>;
	completedRetry?: { id: string; draftText: string };
	onSteer(text: string): Promise<string>;
	onPromoteQueuedTurn(turnId: string): Promise<void>;
	onCancelQueuedTurn(turnId: string): Promise<void>;
	onInterrupt(): void;
	onSettings(settings: TurnSettings): Promise<void>;
	onConfigOption(id: string, value: { value: string } | { enabled: boolean }): Promise<ChatConfigOption[]>;
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
	/** The account-quota banner is up, so the context meter stands down. */
	quotaActive?: boolean;
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
}) {
	const t = useTheme();
	const router = useRouter();
	const styles = useThemedStyles(makeStyles);
	const iconFeedback = usePressFeedback("icon");
	const buttonFeedback = usePressFeedback("button");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	// The keyboard's own progress, 0 closed to 1 open. This is the same value the
	// keyboard is animating with, so the dock moves in lockstep with it.
	const keyboard = useReanimatedKeyboardAnimation();
	const reviewPromptAvailable = Boolean(reviewPR && onOpenReview);
	const dockRise = useAnimatedStyle(() => ({
		transform: [{ translateY: (restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value) }],
	}));
	const [text, setText] = useState("");
	const [quotes, setQuotes] = useState<ResponseQuote[]>([]);
	const inputRef = useRef<TextInput>(null);
	const consumedQuote = useRef<string | undefined>(undefined);
	const [draftLoaded, setDraftLoaded] = useState(false);
	const [cursor, setCursor] = useState(0);
	const [fieldHeight, setFieldHeight] = useState(COMPOSER_FIELD_HEIGHT);
	const [attachments, setAttachments] = useState<Attachment[]>([]);
	const [localError, setLocalError] = useState<string>();
	const [submitting, setSubmitting] = useState(false);
	const [focused, setFocused] = useState(false);
	const [promotingQueuedTurnId, setPromotingQueuedTurnId] = useState<string>();
	const [cancellingQueuedTurnId, setCancellingQueuedTurnId] = useState<string>();
	const [hiddenQueuedTurnIds, setHiddenQueuedTurnIds] = useState<Set<string>>(() => new Set());
	const active = Boolean(activeTurn(snapshot));
	const queuedMessages = useMemo(() => queuedConversationMessages(snapshot, openingTurnId), [openingTurnId, snapshot]);
	const visibleQueuedMessages = useMemo(() => queuedMessages.filter((entry) => !hiddenQueuedTurnIds.has(entry.turnId)), [hiddenQueuedTurnIds, queuedMessages]);
	// Absent below 70%: a permanent token gauge is chrome nobody reads.
	const contextMeter = contextMeterModel(snapshot.usage, Boolean(quotaActive));
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
	const voice = useVoiceInput({ onTranscript: useCallback((spoken: string) => setText((old) => old ? `${old} ${spoken}` : spoken), []) });
	const hasDraft = Boolean(text.trim() || quotes.length);
	const listening = voice.state === "starting" || voice.state === "recording";
	const transcribing = voice.state === "transcribing";
	// Collapsed, the composer is a slim pill under the session's chips. Writing
	// grows it into a card that adds the turn settings between attach and the
	// round button, and it stays open while anything waits to be sent and while
	// the mic is open.
	const expanded = focused || hasDraft || attachments.length > 0 || listening || transcribing;
	// 0 collapsed, 1 open. Everything that changes as the pill opens reads it, so
	// the row, the field's insets and the settings move as one.
	const reduceMotion = useReducedMotion();
	const openness = useSharedValue(expanded ? 1 : 0);
	useEffect(() => { onExpandedChange?.(expanded); }, [expanded, onExpandedChange]);
	useEffect(() => {
		openness.value = reduceMotion ? withTiming(expanded ? 1 : 0, { duration: 0 }) : withSpring(expanded ? 1 : 0, COMPOSER_SPRING);
	}, [expanded, openness, reduceMotion]);
	const primaryAction = composerPrimaryAction({ active, hasDraft, hasAttachments: attachments.length > 0 });
	const steerEligible = Boolean(canSteer && hasDraft && attachments.length === 0 && quotes.length === 0);
	const deliveryPresentation = composerDeliveryPresentation({ active, canSteer: Boolean(canSteer && quotes.length === 0), hasDraft, hasAttachments: attachments.length > 0, hasQueued: visibleQueuedMessages.length > 0 });
	const stopped = snapshot.controller.state === "stopped";
	const draftKey = config ? `ao.chat.draft.${machineIdentity(config)}.${sessionId}` : null;
	const draftTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const openingSuggestion = useRef<string | undefined>(undefined);
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
		const matches = currentQuotes.length <= 8 && currentQuotes.every((quote) => isChatExcerpt({ ...quote.source, text: quote.text })) && excerptDraftText(responseReplyText(current, currentQuotes), responseExcerpts(currentQuotes)) === completedRetry.draftText;
		if (matches) {
			latestText.current = "";
			setText("");
			latestQuotes.current = [];
			setQuotes([]);
			setAttachments([]);
		}
		void (async () => {
			if (draftKey) {
				await afterDraftWrites(draftKey, () => matches ? AsyncStorage.removeItem(draftKey) : AsyncStorage.setItem(draftKey, encodeResponseDraft(current, currentQuotes)));
			}
			await onAcknowledgeSend(completedRetry.id);
		})().catch((cause) => setLocalError(userFacingError(cause)));
	}, [completedRetry, draftKey, onAcknowledgeSend]);


	const submit = useCallback(async (intent: ComposerDeliveryIntent = "send") => {
		if (submitting || pending || disabled || (draftKey && !draftLoaded)) return;
		const trimmed = responseReplyText(text, quotes);
		if (!trimmed && attachments.length === 0) return;
		// Dismissed on the tap, not after the send lands. Waiting for the request
		// tied the keyboard's exit to the network, so it dropped whenever the
		// answer arrived — which read as the keyboard being taken away mid-sentence.
		Keyboard.dismiss();
		setLocalError(undefined);
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
			const id = route === "steer"
				? await onSteer(trimmed)
				: await onSend(trimmed, images.length ? images : undefined, resources.length ? resources : undefined, excerpts.length ? excerpts : undefined);
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
				setFieldHeight(COMPOSER_FIELD_HEIGHT);
				latestQuotes.current = [];
				setQuotes([]);
				setAttachments([]);
			}
			haptics.success();
		} catch (cause) {
			setLocalError(userFacingError(cause));
			haptics.error();
		} finally { setSubmitting(false); }
	}, [text, quotes, attachments, steerEligible, onSteer, onSend, onAcknowledgeSend, draftKey, draftLoaded, submitting, pending, disabled]);

	const addImage = async () => {
		setLocalError(undefined);
		try {
			const result = await ImagePicker.launchImageLibraryAsync({ mediaTypes: ["images"], base64: true, quality: 0.82, allowsMultipleSelection: true, selectionLimit: 4 });
			if (result.canceled) return;
			const errors = new Set<string>();
			const next = result.assets.flatMap((asset): Attachment[] => {
				if (!asset.base64) { errors.add("Some images couldn't be read and were skipped."); return []; }
				const mimeType = (asset.mimeType || "image/jpeg").toLowerCase();
				if (!SUPPORTED_IMAGE_TYPES.has(mimeType)) { errors.add("Only PNG, JPEG, GIF, WebP, and BMP images are supported."); return []; }
				const bytes = asset.fileSize ?? Math.floor(asset.base64.length * 0.75);
				if (bytes > MAX_IMAGE_BYTES) { errors.add("Each image must be under 10 MB."); return []; }
				return [{ id: `${asset.assetId ?? asset.uri}-${Date.now()}`, kind: "image", name: asset.fileName || "Image", bytes, image: { mimeType, data: asset.base64 } }];
			});
			const accepted = [...attachments];
			let imageBytes = accepted.filter((item) => item.kind === "image").reduce((sum, item) => sum + item.bytes, 0);
			for (const item of next) {
				if (accepted.length >= MAX_ATTACHMENTS) { errors.add(`You can attach up to ${MAX_ATTACHMENTS} items.`); break; }
				if (imageBytes + item.bytes > MAX_IMAGE_BYTES_TOTAL) { errors.add("Images must total under 25 MB."); break; }
				accepted.push(item);
				imageBytes += item.bytes;
			}
			setAttachments(accepted);
			setLocalError(errors.size ? [...errors].join(" ") : undefined);
		} catch (cause) {
			setLocalError(userFacingError(cause, "Couldn't open your photo library."));
		}
	};
	const addFile = async () => {
		setLocalError(undefined);
		const result = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true, type: ["text/*", "application/json", "application/xml", "application/yaml"] });
		if (result.canceled) return;
		try {
			const added: Attachment[] = [];
			for (const asset of result.assets) {
				if (attachments.length + added.length >= MAX_ATTACHMENTS) throw new Error(`You can attach up to ${MAX_ATTACHMENTS} items.`);
				if ((asset.size ?? 0) > MAX_EMBEDDED_FILE_BYTES) throw new Error(`${asset.name} is larger than 500 KB. Reference a worktree file with @ instead.`);
				const body = await fetch(asset.uri).then((response) => response.text());
				const bytes = new TextEncoder().encode(body).byteLength;
				if (bytes > MAX_EMBEDDED_FILE_BYTES) throw new Error(`${asset.name} is too large to embed. Reference a worktree file with @ instead.`);
				added.push({ id: `${asset.uri}-${Date.now()}`, kind: "resource", name: asset.name, bytes, resource: { uri: `mobile-attachment://${encodeURIComponent(asset.name)}`, name: asset.name, mimeType: asset.mimeType || "text/plain", text: body } });
			}
			setAttachments((old) => [...old, ...added]);
		} catch (cause) { setLocalError(userFacingError(cause)); }
	};

	const openPicker = useCallback(async (kind: "skills" | "files", activeTrigger?: ComposerSuggestion) => {
		const request = pickerGate.begin();
		const loadedSkills = kind === "skills" ? await onLoadSkills() : skills;
		const loadedFiles = kind === "files" ? await onLoadFiles() : { paths: filePaths, truncated: Boolean(filePathsTruncated) };
		if (!pickerGate.isCurrent(request)) return;
		if (activeTrigger) {
			const currentTrigger = findComposerSuggestion(latestText.current, latestCursor.current);
			if (!currentTrigger || composerSuggestionKey(currentTrigger) !== composerSuggestionKey(activeTrigger)) return;
		}
		const pickerCatalog = kind === "skills"
			? { kind, skills: loadedSkills } as const
			: { kind, paths: loadedFiles.paths } as const;
		router.push(chatSheetRoute({ kind: "composer-picker", catalog: pickerCatalog, initialQuery: activeTrigger?.query, truncated: kind === "files" ? loadedFiles.truncated : undefined, onSelect: (value) => {
			setText((old) => {
				const next = activeTrigger ? replaceComposerSuggestion(old, activeTrigger, value) : `${old}${old && !/\s$/.test(old) ? " " : ""}${kind === "skills" ? `/${value}` : (/\s/.test(value) ? `"${value}"` : value)} `;
				setCursor(next.length);
				return next;
			});
		} }));
	}, [filePaths, filePathsTruncated, onLoadFiles, onLoadSkills, pickerGate, router, skills]);
	useEffect(() => {
		const suggestion = findComposerSuggestion(text, cursor);
		if (!suggestion) {
			pickerGate.invalidate();
			openingSuggestion.current = undefined;
			return;
		}
		const key = composerSuggestionKey(suggestion);
		if (openingSuggestion.current === key) return;
		openingSuggestion.current = key;
		void openPicker(suggestion.kind, suggestion);
	}, [cursor, openPicker, pickerGate, text]);
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
	const attachMenu = <ChatAttachmentMenu disabled={stopped || submitting} canAttachFile={Boolean(canEmbedFiles)} onChoosePhoto={() => void addImage()} onChooseFile={() => void addFile()} />;
	const canDictate = voice.state !== "unavailable" && voice.state !== "denied";
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
				accessibilityLabel={primary === "stopDictation" ? "Stop dictating" : primary === "transcribing" ? "Transcribing" : "Dictate a message"}
				accessibilityState={{ disabled: stopped || submitting || transcribing, busy: transcribing }}
				disabled={stopped || submitting || transcribing}
				android_ripple={sendFeedback.android_ripple}
				onPress={() => { if (primary === "dictate") Keyboard.dismiss(); voice.toggleLatched(); }}
				style={({ pressed }) => [styles.roundTarget, pressed && sendFeedback.pressed, (stopped || submitting) && styles.inactive]}
			>
				<View style={[styles.roundDisc, primary === "stopDictation" ? styles.dictating : styles.roundAccent]}>
					{transcribing ? <ActivityIndicator size="small" color={t.onAccent} /> : <AppSymbol name={primary === "stopDictation" ? "stop" : dictateGlyph} size={iconSize.sm} weight="bold" color={primary === "stopDictation" ? t.onDestructive : t.onAccent} />}
				</View>
			</Pressable>;
	// The corner controls stay where they are; the pill grows up around them. The
	// field starts between them and is let out to the pill's full width, and the
	// controls row opens under it, its settings rising into place.
	const fieldInsets = useAnimatedStyle(() => ({
		paddingLeft: interpolate(openness.value, [0, 1], [CORNER_CONTROL, space.sm]),
		paddingRight: interpolate(openness.value, [0, 1], [CORNER_CONTROL, space.sm]),
	}));
	const controlsOpening = useAnimatedStyle(() => ({
		height: interpolate(openness.value, [0, 1], [0, touchTarget], "clamp"),
		opacity: interpolate(openness.value, [0.35, 1], [0, 1], "clamp"),
		transform: [{ translateY: interpolate(openness.value, [0, 1], [space.sm, 0], "clamp") }],
	}));
	// The session's state as chips over the composer, open or not: its PR, what
	// it changed, and how full the context is once that matters.
	const chips = activeReviewPR || changes || contextMeter ? <Animated.View entering={FadeIn.duration(COMPOSER_FADE_MS)} exiting={FadeOut.duration(COMPOSER_FADE_MS)}>
		<ScrollView horizontal showsHorizontalScrollIndicator={false} keyboardShouldPersistTaps="handled" contentContainerStyle={styles.chips}>
			{activeReviewPR ? <ComposerChip accessibilityLabel={prChipAccessibilityLabel(activeReviewPR, reviewPRSummary)} accessibilityHint="Opens the pull request review" onPress={onOpenReview}>
				<PRChipContent pr={activeReviewPR} summary={reviewPRSummary} />
			</ComposerChip> : null}
			{changes ? <ComposerChip accessibilityLabel={changesAccessibilityLabel(changes)} accessibilityHint="Opens the changed files" onPress={changes.onOpen}>
				<AppSymbol name="diff" size={iconSize.xs} color={t.textSecondary} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.chipText}>{changes.files}</Text>
				{changes.additions ? <Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.chipCount, { color: t.green }]}>+{compactCount(changes.additions)}</Text> : null}
				{changes.deletions ? <Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.chipCount, { color: t.red }]}>−{compactCount(changes.deletions)}</Text> : null}
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
	// While a turn runs, a message waits for it: the placeholder says so until
	// there is a draft, and this note after that.
	const deliveryNote = deliveryPresentation.showQueueNote && !steerAction ? <View style={styles.deliveryNote}>
		<AppSymbol name={sentLater} size={iconSize.xs} color={t.textTertiary} />
		<Text numberOfLines={1} style={styles.deliveryNoteText} maxFontSizeMultiplier={fontScaleCap.chrome}>{attachments.length ? "Attachments next" : "Sent after this"}</Text>
	</View> : null;
	return (
		// The dock holds its resting inset at all times and rides the keyboard's own
		// progress to close the difference, so its distance to the keyboard is
		// `KEYBOARD_DOCK_GAP` at every frame of the animation rather than only once
		// the keyboard has finished moving. It floats over the conversation with no
		// fill of its own: the chips and the composer carry their own glass.
		<Animated.View style={[styles.dock, styles.floatingDock, { paddingBottom: restingInset }, dockRise]}>
			<DockFade />
			{chips}
			{voice.state === "starting" || voice.state === "recording" ? <View style={styles.voice}><AppSymbol name="mic" size={iconSize.xs} color={t.red} /><Text style={styles.voiceText}>{voice.partial || (voice.state === "starting" ? "Keep holding…" : "Listening…")}</Text></View> : null}
			{attachments.length ? <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.attachments}>{attachments.map((item) => <ComposerAttachment key={item.id} name={item.name} image={item.kind === "image" ? item.image : undefined} onRemove={() => setAttachments((old) => old.filter((candidate) => candidate.id !== item.id))} />)}</ScrollView> : null}
			{/* Composer-local only. Conversation and action failures are banners above
			    the timeline; echoing them here showed one failure twice. */}
			{localError || voice.error ? <Text accessibilityRole="alert" style={styles.error}>{localError || voice.error}</Text> : null}
			{visibleQueuedMessages.length ? <View accessibilityRole="list" style={styles.queueDock}>
				{visibleQueuedMessages.map((entry, index) => {
					const promoting = promotingQueuedTurnId === entry.turnId;
					const cancelling = cancellingQueuedTurnId === entry.turnId;
					const queueActionPending = Boolean(promotingQueuedTurnId || cancellingQueuedTurnId);
					return <View key={entry.turnId} style={[styles.queueRow, index > 0 && styles.queueRowDivider]}>
						<AppSymbol name={queued} size={iconSize.sm} color={t.textTertiary} />
						<Text numberOfLines={1} style={styles.queueText}>{entry.message.text}</Text>
						{canSteer ? <Pressable
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
						<Pressable
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
						</Pressable>
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
				{/* The field keeps one place in the tree whatever the pill's shape, so it
				    keeps its focus as the pill opens around it. */}
				<Animated.View style={[styles.composerRow, fieldInsets]}>
					<TextInput
						ref={inputRef}
						accessibilityLabel="Message the agent"
						editable={!stopped && !submitting}
						value={text}
						onChangeText={(value) => { latestText.current = value; setText(value); }}
						onSelectionChange={(event) => setCursor(event.nativeEvent.selection.start)}
						onFocus={() => setFocused(true)}
						onBlur={() => setFocused(false)}
						onContentSizeChange={(event) => {
							if (!latestText.current) {
								setFieldHeight(COMPOSER_FIELD_HEIGHT);
								return;
							}
							// Native contentSize already includes the TextInput's vertical
							// padding. Treat it as the field's full height; converting it to
							// lines counted the padding as an extra line on the first render.
							const contentHeight = Math.ceil(event.nativeEvent.contentSize.height);
							setFieldHeight(Math.max(COMPOSER_FIELD_HEIGHT, Math.min(COMPOSER_FIELD_MAX_HEIGHT, contentHeight)));
						}}
						placeholder={stopped ? "Agent is stopped" : startingAgentName ? `${startingAgentName} is starting…` : deliveryPresentation.placeholder}
						placeholderTextColor={t.textFaint}
						{...textSelectionColors(t)}
						style={[styles.input, { height: text ? fieldHeight : COMPOSER_FIELD_HEIGHT }]}
						multiline
						maxLength={40_000}
					/>
				</Animated.View>
				{/* Mounted closed too, at no height, so the settings are already
				    drawn when the row opens rather than appearing partway through. */}
				<Animated.View pointerEvents={expanded ? "box-none" : "none"} style={[styles.controlsRow, { paddingLeft: CORNER_CONTROL, paddingRight: CORNER_CONTROL }, controlsOpening]}>
					<View style={styles.settingsSlot}>
						<ChatTurnSettingsControl snapshot={snapshot} models={models} options={configOptions ?? []} disabled={disabled || stopped || pending || submitting} onSettings={onSettings} onOption={onConfigOption} />
					</View>
					{deliveryNote}
					{steerAction}
				</Animated.View>
				{/* The corner controls, pinned to the pill's bottom corners in both
				    shapes. The round button changes face in place, as the new-task
				    sheet's does. */}
				<View style={[styles.corner, styles.cornerLeading]}>{attachMenu}</View>
				<View style={[styles.corner, styles.cornerTrailing]}>
					{roundButton}
				</View>
			</View>}
		</Animated.View>
	);
}

/**
 * The conversation fading out behind the dock, as a system bar's scroll edge
 * does: clear above it, the page's colour by the time it reaches the composer,
 * so text running under the chips and the pill never competes with them.
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
						<Stop offset="0.5" stopColor={color} stopOpacity={0.85} />
						<Stop offset="1" stopColor={color} stopOpacity={1} />
					</LinearGradient>
				</Defs>
				<Rect x="0" y="0" width="100%" height="100%" fill="url(#dockFade)" />
			</Svg>
		</View>
		<View style={{ flex: 1, backgroundColor: color }} />
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
	// The card's controls: attach, the turn settings, then the delivery note or
	// steer, dictation and the control that commits. The settings label is the
	// only one that can be long, so it is the one that yields: `flex: 1` with
	// `minWidth: 0` to allow the squeeze, and `overflow: "hidden"` so a label that
	// does not truncate cleanly cannot paint over its neighbours. Both are at
	// least a touch target tall, so the trigger's target is never clipped.
	controlsRow: { width: "100%", flexDirection: "row", alignItems: "center", gap: space.xxs, overflow: "hidden" },
	settingsSlot: { flex: 1, minWidth: 0, minHeight: touchTarget, alignItems: "flex-start", justifyContent: "center", overflow: "hidden" },
	chips: { gap: space.xs, paddingVertical: CHIP_SLOP },
	// A capsule; the glass fills it on iOS 26, so the fill drops out there.
	chip: { height: CHIP_HEIGHT, flexDirection: "row", alignItems: "center", gap: space.xxs, paddingHorizontal: space.md, borderRadius: radius.pill, borderCurve: "continuous", overflow: "hidden", backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated, borderWidth: composerGlassSupported ? 0 : rowDividerWidth, borderColor: t.borderDefault },
	chipText: { fontFamily: "DMSans_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600" },
	chipCount: { fontFamily: t.fontMono, fontSize: type.footnote.fontSize },
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
	roundDisc: { width: COMPOSER_DISC, height: COMPOSER_DISC, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center" },
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
	// As tall as the row; it yields before the controls do.
	deliveryNote: { flexShrink: 1, minWidth: 0, height: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xxs },
	deliveryNoteText: { fontFamily: "DMSans_600SemiBold", flexShrink: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600" },
	steerAction: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
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
