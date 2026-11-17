import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { machineIdentity, type ServerConfig } from "../config";
import {
	cancelQueuedConversationTurn,
	compactConversation,
	editConversationMessage,
	getConversationConfigOptions,
	getConversationModels,
	getConversationPage,
	getReviewerConversationPage,
	getConversationSkills,
	mergeConversationPages,
	reloadMcpServers,
	resolveApproval,
	resolveReviewerApproval,
	resolveInput,
	resolveReviewerInput,
	rollbackConversation,
	sendConversationMessage,
	switchProviderAndSend,
	type ProviderSwitchInput,
	requireConversationExcerptSupport,
	sendReviewerConversationMessage,
	setConversationConfigOption,
	setConversationSettings,
	setConversationTitle,
	stageConversationAttachments,
	steerConversation,
	interruptConversation,
	interruptReviewerConversation,
	promoteQueuedConversationTurn,
	recoverSentConversationMessage,
	type ConversationPage,
} from "./api";
import type { ChatConfigOption, ChatExcerpt, ChatImage, ChatModel, ChatResource, ChatSkill, ConversationSnapshot, TurnSettings } from "./types";
import { cachedConversationState, createMobileConversationPageCache, discardHistoricalPages, olderPageFits, withLivePage } from "./snapshot";
import { conversationActionError, conversationErrorCode, conversationErrorIsPermanent, ignoreStaleApproval } from "./conversationErrors";
import { subscribeConversationEvents } from "./conversationEvents";
import { conversationPollIntervalFor } from "./conversationPoll";
import { createAsyncValueCache } from "./asyncValueCache";
import { createRequestGate } from "./requestGate";
import { withAttachmentReferences } from "./messageAttachments";
import { isEmptyTurnOptionCatalog, loadTurnOptionCatalog } from "./turnOptionsCatalog";
import { rememberedTurnOptions, rememberTurnOptions, type TurnOptions } from "./lastTurnOptions";
import { NOT_PAIRED_ACTION_COPY, RECONNECTING_ACTION_COPY } from "../connectionError";
import { excerptDraftText } from "./responseQuotes";
import { clearPendingSend, needsAttachmentRecovery, pendingSendKey, readPendingSend, reservePendingSend } from "./pendingSend";

const REFRESH_DEBOUNCE_MS = 120;
const conversationPageCache = createMobileConversationPageCache();
const turnOptionsCache = createAsyncValueCache<string, TurnOptions>(16, 30_000);
const skillsCache = createAsyncValueCache<string, ChatSkill[]>(16, 30_000);

export type PendingSend = {
	id: string;
	draftText: string;
	text: string;
	kind?: "send" | "steer";
	state: "sending" | "failed";
	error?: string;
	attachments?: ChatImage[];
	resources?: ChatResource[];
	excerpts?: ChatExcerpt[];
	hasAttachments: boolean;
	restored?: boolean;
	/** A send that also switches the chat's agent; see switchProviderAndSend. */
	providerSwitch?: ProviderSwitchInput;
};

export type ConversationAction =
	| "steer"
	| "queue"
	| "interrupt"
	| "approval"
	| "input"
	| "compact"
	| "rollback"
	| "edit"
	| "settings"
	| "config"
	| "mcp"
	| "rename";

export type MobileConversation = {
	snapshot?: ConversationSnapshot;
	loading: boolean;
	refreshing: boolean;
	loadingOlder: boolean;
	error?: string;
	unavailable?: { code?: string; message: string };
	models: ChatModel[];
	configOptions: ChatConfigOption[];
	skills: ChatSkill[];
	pendingSends: PendingSend[];
	completedRetry?: { id: string; draftText: string };
	pendingActions: readonly ConversationAction[];
	actionError?: string;
	actionErrors: Partial<Record<ConversationAction, string>>;
	actionCodes: Partial<Record<ConversationAction, string>>;
	refresh(): Promise<void>;
	loadOlder(): Promise<void>;
	/**
	 * `ifMissing` loads only when this chat has no catalog of its own yet: the
	 * last load failed or came back empty, so what shows is remembered or nothing.
	 */
	loadTurnOptions(options?: { refresh?: boolean; ifMissing?: boolean }): Promise<{ models: ChatModel[]; configOptions: ChatConfigOption[] }>;
	loadSkills(): Promise<ChatSkill[]>;
	/** With `providerSwitch`, the chat switches to that agent and sends to it in one request. */
	send(text: string, attachments?: ChatImage[], resources?: ChatResource[], excerpts?: ChatExcerpt[], providerSwitch?: ProviderSwitchInput): Promise<string>;
	retrySend(id: string): Promise<void>;
	discardSend(id: string): Promise<void>;
	acknowledgeSend(id: string): Promise<void>;
	steer(text: string): Promise<string>;
	promoteQueuedTurn(turnId: string): Promise<void>;
	cancelQueuedTurn(turnId: string): Promise<void>;
	interrupt(): Promise<void>;
	resolveApproval(requestId: string, decisionId: string): Promise<void>;
	resolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	compact(): Promise<void>;
	rollback(turnId: string): Promise<number>;
	/** Retry an uncertain edit with the same `clientMessageId`; the daemon will not branch it twice. */
	editMessage(turnId: string, text: string, clientMessageId: string): Promise<void>;
	chooseSettings(settings: TurnSettings): Promise<void>;
	setConfigOption(optionId: string, value: { value: string } | { enabled: boolean }): Promise<ChatConfigOption[]>;
	reloadMcp(): Promise<void>;
	rename(title: string): Promise<void>;
};

/**
 * `verified` is the connection, used only to make requests; it is null in
 * the background and while endpoints are raced. `machine` (the store's, see
 * viewMachine.ts) says whose conversation this is, and keys everything shown:
 * keyed on the connection, every app switch swapped the transcript and pending sends
 * for an empty key and back.
 */
export function useMobileConversation(
	verified: ServerConfig | null,
	machine: string,
	sessionId: string,
	options?: { reviewId?: string; eventSessionId?: string },
): MobileConversation {
	const scope = options?.reviewId ? `review:${options.reviewId}` : sessionId;
	const cacheKey = machine ? conversationPageCacheKey(machine, scope) : "";
	const pendingKey = machine ? pendingSendKey(machine, scope) : "";
	// A connection to some other machine (mid-switch) must not answer for this one.
	const cfg = verified && machineIdentity(verified) === machine ? verified : null;
	// Why an action cannot be sent right now. A paired machine without a
	// connection is reconnecting, not unpaired: the screen stays up through an
	// app switch and an endpoint race, so actions can be tried in that window.
	const offline = cfg ? "" : machine ? RECONNECTING_ACTION_COPY : NOT_PAIRED_ACTION_COPY;
	const [initialState] = useState(() => cacheKey
		? cachedConversationState(conversationPageCache, cacheKey)
		: { pages: [] as ConversationPage[], loading: true });
	const [pages, setPages] = useState<ConversationPage[]>(initialState.pages);
	const [loading, setLoading] = useState(initialState.loading);
	const [refreshing, setRefreshing] = useState(false);
	const [loadingOlder, setLoadingOlder] = useState(false);
	const [error, setError] = useState<string>();
	const [unavailable, setUnavailable] = useState<{ code?: string; message: string }>();
	// The remembered catalog of the agent this chat last ran, never another's: a
	// chat that switched agents keeps the outgoing agent's catalog remembered.
	const [initialCatalog] = useState(() => rememberedTurnOptions(cacheKey, initialState.pages.length ? mergeConversationPages(initialState.pages)?.harness : undefined));
	const [models, setModels] = useState<ChatModel[]>(() => initialCatalog?.models ?? []);
	const [configOptions, setConfigOptions] = useState<ChatConfigOption[]>(() => initialCatalog?.configOptions ?? []);
	const [skills, setSkills] = useState<ChatSkill[]>([]);
	const [pendingSends, setPendingSends] = useState<PendingSend[]>([]);
	const [completedRetry, setCompletedRetry] = useState<{ id: string; draftText: string }>();
	const [pendingActions, setPendingActions] = useState<ConversationAction[]>([]);
	const [actionError, setActionError] = useState<string>();
	const [actionErrors, setActionErrors] = useState<Partial<Record<ConversationAction, string>>>({});
	const [actionCodes, setActionCodes] = useState<Partial<Record<ConversationAction, string>>>({});
	const mounted = useRef(true);
	// Whether the catalog shown came from a load of this chat, not from memory.
	const turnOptionsLoaded = useRef(false);
	// Only the newest catalog load decides what shows: an older one that fails or
	// lands late must not undo it. `ifMissing` waits on the one in flight.
	const turnOptionsRequest = useRef(0);
	const turnOptionsInFlight = useRef<Promise<unknown> | undefined>(undefined);
	const refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const refreshGate = useRef(createRequestGate()).current;

	const snapshot = useMemo(() => mergeConversationPages(pages), [pages]);
	// The agent the chat runs, for a retried switch's send (see deliver).
	const harnessRef = useRef(snapshot?.harness);
	harnessRef.current = snapshot?.harness;
	const hasConversation = Boolean(snapshot);
	// A new agent's catalog starts empty: the outgoing agent's models and
	// options are never offered for it, even when its first load fails. Loads
	// started for the outgoing agent no longer count (desktop's
	// clearConversationProviderCatalogs).
	const harnessShown = useRef<{ cacheKey: string; harness: string } | undefined>(undefined);
	const snapshotHarness = snapshot?.harness;
	useEffect(() => {
		if (!snapshotHarness) return;
		const previous = harnessShown.current;
		harnessShown.current = { cacheKey, harness: snapshotHarness };
		if (previous?.cacheKey !== cacheKey) {
			// The first snapshot names the agent: a remembered catalog shown before
			// it (no cached snapshot to check against) must be that agent's.
			if (!turnOptionsLoaded.current && !rememberedTurnOptions(cacheKey, snapshotHarness)) {
				setModels([]);
				setConfigOptions([]);
			}
			return;
		}
		if (previous.harness === snapshotHarness) return;
		turnOptionsRequest.current++;
		turnOptionsLoaded.current = false;
		turnOptionsCache.delete(cacheKey);
		setModels([]);
		setConfigOptions([]);
	}, [cacheKey, snapshotHarness]);

	const refresh = useCallback(async () => {
		if (!cfg) return;
		const request = refreshGate.begin();
		setRefreshing(true);
		try {
			const live = options?.reviewId
				? await getReviewerConversationPage(cfg, options.reviewId)
				: await getConversationPage(cfg, sessionId);
			if (!mounted.current || !refreshGate.isCurrent(request)) return;
			setPages((old) => {
				const next = withLivePage(old, live);
				conversationPageCache.set(cacheKey, next);
				return next;
			});
			setUnavailable(undefined);
			setError(undefined);
		} catch (cause) {
			if (!mounted.current || !refreshGate.isCurrent(request)) return;
			const classified = classifyConversationError(cause, Boolean(options?.reviewId));
			if (classified.permanent) setUnavailable({ code: classified.code, message: classified.message });
			else setError(classified.message);
		} finally {
			if (mounted.current && refreshGate.isCurrent(request)) {
				setLoading(false);
				setRefreshing(false);
			}
		}
	}, [cacheKey, cfg, options?.reviewId, refreshGate, sessionId]);

	const scheduleRefresh = useCallback(() => {
		if (refreshTimer.current) clearTimeout(refreshTimer.current);
		refreshTimer.current = setTimeout(() => void refresh(), REFRESH_DEBOUNCE_MS);
	}, [refresh]);

	const loadOlder = useCallback(async () => {
		if (!cfg || !snapshot?.hasMoreBefore || loadingOlder) return;
		setLoadingOlder(true);
		try {
			const older = options?.reviewId
				? await getReviewerConversationPage(cfg, options.reviewId, snapshot.oldestSequence)
				: await getConversationPage(cfg, sessionId, snapshot.oldestSequence);
			if (mounted.current) setPages((old) => olderPageFits(old, older) ? [...old, older] : old);
		} catch (cause) {
			if (mounted.current) setActionError(conversationActionError(cause));
		} finally {
			if (mounted.current) setLoadingOlder(false);
		}
	}, [cfg, options?.reviewId, sessionId, snapshot?.hasMoreBefore, snapshot?.oldestSequence, loadingOlder]);

	// What is remembered for this conversation shows as soon as it is known,
	// and only a different conversation replaces it. Losing the connection
	// does not: the transcript stays and the next refresh brings it up to date.
	useEffect(() => {
		const cached = cacheKey
			? cachedConversationState(conversationPageCache, cacheKey)
			: { pages: [] as ConversationPage[], loading: true };
		setPages(cached.pages);
		setLoading(cached.loading);
		setUnavailable(undefined);
	}, [cacheKey]);

	// Asks the machine whenever a verified connection to it is (again) available.
	useEffect(() => {
		mounted.current = true;
		void refresh();
		return () => {
			mounted.current = false;
			refreshGate.invalidate();
			if (refreshTimer.current) clearTimeout(refreshTimer.current);
		};
	}, [refresh, refreshGate]);

	useEffect(() => {
		setPendingSends([]);
		setCompletedRetry(undefined);
		if (!pendingKey) return;
		let active = true;
		void readPendingSend(pendingKey).then((saved) => {
			if (active && saved) setPendingSends((old) => upsertPending(old, {
				...saved, state: "failed", restored: true,
				error: options?.reviewId && saved.hasAttachments
					? "Check review history. If the reply is absent, discard it here, then reattach the files and send again."
					: "Delivery may have succeeded. Retry with the same message or discard it after checking the conversation.",
			}));
		}).catch((cause) => { if (active) setError(conversationActionError(cause)); });
		return () => { active = false; };
	}, [pendingKey, options?.reviewId]);

	const loadTurnOptions = useCallback(async (options?: { refresh?: boolean; ifMissing?: boolean }) => {
		if (!cfg || unavailable || !hasConversation) return { models, configOptions };
		if (options?.ifMissing) {
			await turnOptionsInFlight.current?.catch(() => {});
			if (turnOptionsLoaded.current) return rememberedTurnOptions(cacheKey, harnessRef.current) ?? { models, configOptions };
		}
		const request = ++turnOptionsRequest.current;
		// The agent this load is for: its answer is applied and remembered for it alone.
		const harness = harnessRef.current;
		if (options?.refresh) turnOptionsCache.delete(cacheKey);
		// An empty catalog is usually a provider that is not up yet: it is not
		// cached, so the next load asks again, and what it offered before stays.
		const loading = turnOptionsCache.load(cacheKey, async () => {
			return loadTurnOptionCatalog({
				loadModels: () => getConversationModels(cfg, sessionId),
				loadConfigOptions: () => getConversationConfigOptions(cfg, sessionId),
			});
		}, (loaded) => !isEmptyTurnOptionCatalog(loaded));
		turnOptionsInFlight.current = loading;
		let catalog: TurnOptions;
		try {
			catalog = await loading;
		} catch (cause) {
			if (request === turnOptionsRequest.current) turnOptionsLoaded.current = false;
			throw cause;
		} finally {
			if (turnOptionsInFlight.current === loading) turnOptionsInFlight.current = undefined;
		}
		const empty = isEmptyTurnOptionCatalog(catalog);
		// Only the same agent's: after an agent switch the outgoing agent's
		// models would be wrong, so an empty catalog then shows empty.
		const shown = empty ? rememberedTurnOptions(cacheKey, harness) ?? catalog : catalog;
		if (request !== turnOptionsRequest.current || harnessRef.current !== harness) return shown;
		turnOptionsLoaded.current = !empty;
		if (!empty) rememberTurnOptions(cacheKey, catalog, harness);
		if (mounted.current) {
			setModels(shown.models);
			setConfigOptions(shown.configOptions);
		}
		return shown;
	}, [cacheKey, cfg, configOptions, hasConversation, models, sessionId, unavailable]);

	const loadSkills = useCallback(async () => {
		if (!cfg || unavailable || !hasConversation) return skills;
		try {
			const next = await skillsCache.load(cacheKey, () => getConversationSkills(cfg, sessionId));
			if (mounted.current) setSkills(next);
			return next;
		} catch {
			return skills;
		}
	}, [cacheKey, cfg, hasConversation, sessionId, skills, unavailable]);

	useEffect(() => {
		const eventSessionId = options?.eventSessionId ?? sessionId;
		if (!cfg || unavailable || !eventSessionId) return;
		return subscribeConversationEvents(cfg, eventSessionId, (event) => {
			if (event.payload?.conversationId) scheduleRefresh();
		});
	}, [cfg, options?.eventSessionId, sessionId, scheduleRefresh, unavailable]);

	// Poll the conversation on paths where the event stream cannot deliver.
	// Over a Cloudflare quick tunnel the subscription above never fires — the
	// body is forwarded in ~128 KB chunks and a chat event is a few hundred
	// bytes — so without this the screen shows the agent working indefinitely
	// while the reply has already landed.
	useEffect(() => {
		const every = conversationPollIntervalFor(cfg);
		if (every === null || unavailable) return;
		const timer = setInterval(() => scheduleRefresh(), every);
		return () => clearInterval(timer);
	}, [cfg, unavailable, scheduleRefresh]);

	const runAction = useCallback(
		// `banner: false` keeps a failure out of the screen banner when the control
		// that acted already shows it in place (the inline message editor).
		async <T,>(kind: ConversationAction, action: () => Promise<T>, resetHistoricalPages = false, banner = true): Promise<T> => {
			setPendingActions((old) => old.includes(kind) ? old : [...old, kind]);
			setActionError(undefined);
			setActionErrors((old) => ({ ...old, [kind]: undefined }));
			setActionCodes((old) => ({ ...old, [kind]: undefined }));
			try {
				const result = await action();
				if (resetHistoricalPages) setPages(discardHistoricalPages);
				await refresh();
				return result;
			} catch (cause) {
				const message = conversationActionError(cause);
				if (banner) setActionError(message);
				setActionErrors((old) => ({ ...old, [kind]: message }));
				const code = conversationErrorCode(cause);
				setActionCodes((old) => ({ ...old, [kind]: code }));
				throw Object.assign(new Error(message), { code });
			} finally {
				setPendingActions((old) => old.filter((candidate) => candidate !== kind));
			}
		},
		[refresh],
	);

	const deliver = useCallback(
		async (pending: PendingSend) => {
			if (!cfg) throw new Error(offline);
			setPendingSends((old) => upsertPending(old, { ...pending, state: "sending", error: undefined }));
			try {
				if (needsAttachmentRecovery(Boolean(pending.restored), pending.hasAttachments, Boolean(pending.attachments?.length || pending.resources?.length))) {
					if (options?.reviewId) throw new Error("Check review history first. If the reply is absent, discard it here, then reattach the files and send again.");
					try {
						await recoverSentConversationMessage(cfg, sessionId, pending.id);
					} catch (cause) {
						if (conversationErrorCode(cause) === "CHAT_STEER_UNCERTAIN") {
							throw new Error("This attachment message was not found. Discard it here, then reattach the files and send again.");
						}
						throw cause;
					}
				} else if (pending.kind === "steer") {
					await steerConversation(cfg, sessionId, pending.text, pending.id);
				} else {
					const message = {
						text: pending.text,
						clientMessageId: pending.id,
						attachments: pending.attachments,
						resources: pending.resources,
						excerpts: pending.excerpts,
					};
					// A retry after the chat already took the new agent is an ordinary
					// send, as on desktop: the switch went through and only the send did not.
					const providerSwitch = !options?.reviewId && pending.providerSwitch?.targetHarness !== harnessRef.current
						? pending.providerSwitch
						: undefined;
					if (providerSwitch) await switchProviderAndSend(cfg, sessionId, providerSwitch, message);
					else await (options?.reviewId ? sendReviewerConversationMessage : sendConversationMessage)(cfg, options?.reviewId ?? sessionId, message);
				}
				setPendingSends((old) => old.filter((item) => item.id !== pending.id));
				await refresh();
			} catch (cause) {
				const message = conversationActionError(cause);
				if (pending.kind === "steer") setActionCodes((old) => ({ ...old, steer: conversationErrorCode(cause) }));
				setPendingSends((old) => upsertPending(old, { ...pending, state: "failed", error: message }));
				throw new Error(message);
			}
		},
		[cfg, offline, options?.reviewId, sessionId, refresh],
	);

	const send = useCallback(
		async (text: string, attachments?: ChatImage[], resources?: ChatResource[], excerpts?: ChatExcerpt[], providerSwitch?: ProviderSwitchInput) => {
			if (!pendingKey) throw new Error(NOT_PAIRED_ACTION_COPY);
			// Before anything is reserved: a record written here that never reaches
			// `pendingSends` would block the next send with nothing to resolve.
			if (!cfg) throw new Error(offline);
			const id = clientMessageId();
			let message = text;
			let nativeAttachments = attachments;
			if (attachments?.length) {
				if (options?.reviewId && !sessionId.trim()) throw new Error("Attachments are unavailable until the worker session is linked.");
				if (excerpts?.length && !options?.reviewId) {
					await requireConfig(cfg, offline, (c) => requireConversationExcerptSupport(c, sessionId));
				}
				const paths = await requireConfig(cfg, offline, (c) => stageConversationAttachments(c, sessionId, attachments));
				message = withAttachmentReferences(message, paths);
				if (!snapshot?.capabilities?.includes("images")) nativeAttachments = undefined;
			}
			const saved = await reservePendingSend(pendingKey, {
				id, draftText: excerptDraftText(text, excerpts), text: message, excerpts, hasAttachments: Boolean(attachments?.length || resources?.length), kind: "send",
				...(providerSwitch ? { providerSwitch } : {}),
			});
			await deliver({
				...saved, text: attachments?.length || resources?.length ? message : saved.text,
				state: "sending", attachments: nativeAttachments, resources,
				restored: saved.id !== id,
			});
			return saved.id;
		},
		[cfg, offline, options?.reviewId, sessionId, snapshot?.capabilities, deliver, pendingKey],
	);

	const retrySend = useCallback(
		async (id: string) => {
			const pending = pendingSends.find((item) => item.id === id);
			if (pending) {
				await deliver(pending);
				setCompletedRetry({ id, draftText: pending.draftText });
			}
		},
		[pendingSends, deliver],
	);

	const discardSend = useCallback(async (id: string) => {
		if (pendingKey) await clearPendingSend(pendingKey, id);
		setPendingSends((old) => old.filter((item) => item.id !== id));
	}, [pendingKey]);
	const acknowledgeSend = useCallback(async (id: string) => {
		if (pendingKey) await clearPendingSend(pendingKey, id);
		setCompletedRetry((current) => current?.id === id ? undefined : current);
	}, [pendingKey]);
	const steer = useCallback(async (text: string) => {
		if (!pendingKey) throw new Error(NOT_PAIRED_ACTION_COPY);
		if (!cfg) throw new Error(offline);
		const id = clientMessageId();
		const saved = await reservePendingSend(pendingKey, { id, draftText: text, text, hasAttachments: false, kind: "steer" });
		await deliver({ ...saved, state: "sending", restored: saved.id !== id });
		setActionCodes((old) => ({ ...old, steer: undefined }));
		return saved.id;
	}, [cfg, offline, pendingKey, deliver]);
	const cancelQueuedTurn = useCallback(
		(turnId: string) => runAction("queue", () => requireConfig(cfg, offline, (c) => cancelQueuedConversationTurn(c, sessionId, turnId))),
		[cfg, offline, runAction, sessionId],
	);
	const promoteQueuedTurn = useCallback(
		(turnId: string) => runAction("queue", () => requireConfig(cfg, offline, (c) => promoteQueuedConversationTurn(c, sessionId, turnId))),
		[cfg, offline, runAction, sessionId],
	);
	const interrupt = useCallback(
		() => runAction("interrupt", () => requireConfig(cfg, offline, (c) => options?.reviewId
			? interruptReviewerConversation(c, options.reviewId)
			: interruptConversation(c, sessionId))),
		[cfg, offline, options?.reviewId, runAction, sessionId],
	);
	const resolveApprovalAction = useCallback(
		(requestId: string, decisionId: string) =>
			runAction("approval", () => ignoreStaleApproval(() => requireConfig(cfg, offline, (c) => options?.reviewId
				? resolveReviewerApproval(c, options.reviewId, requestId, decisionId)
				: resolveApproval(c, sessionId, requestId, decisionId)))),
		[cfg, offline, options?.reviewId, runAction, sessionId],
	);
	const resolveInputAction = useCallback(
		(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>) =>
			runAction("input", () => requireConfig(cfg, offline, (c) => options?.reviewId
				? resolveReviewerInput(c, options.reviewId, requestId, action, content)
				: resolveInput(c, sessionId, requestId, action, content))),
		[cfg, offline, options?.reviewId, runAction, sessionId],
	);
	const compact = useCallback(
		() => runAction("compact", () => requireConfig(cfg, offline, (c) => compactConversation(c, sessionId))),
		[cfg, offline, runAction, sessionId],
	);
	const rollback = useCallback(
		(turnId: string) => runAction("rollback", () => requireConfig(cfg, offline, (c) => rollbackConversation(c, sessionId, turnId)), true),
		[cfg, offline, runAction, sessionId],
	);
	const editMessage = useCallback(
		(turnId: string, text: string, clientMessageId: string) =>
			runAction("edit", () => requireConfig(cfg, offline, (c) => editConversationMessage(c, sessionId, turnId, text, clientMessageId)), true, false),
		[cfg, offline, runAction, sessionId],
	);
	const chooseSettings = useCallback(
		(settings: TurnSettings) => runAction("settings", () => requireConfig(cfg, offline, (c) => setConversationSettings(c, sessionId, settings))),
		[cfg, offline, runAction, sessionId],
	);
	const setConfigOption = useCallback(
		(optionId: string, value: { value: string } | { enabled: boolean }) =>
			runAction("config", async () => {
				const harness = harnessRef.current;
				const options = await requireConfig(cfg, offline, (c) => setConversationConfigOption(c, sessionId, optionId, value));
				// An agent switch during the request leaves these options the previous
				// agent's: neither shown nor remembered for the new one.
				if (harnessRef.current !== harness) return options;
				// The provider's own post-change catalog: a load still in flight is older.
				turnOptionsRequest.current++;
				setConfigOptions(options);
				turnOptionsCache.set(cacheKey, { models, configOptions: options });
				rememberTurnOptions(cacheKey, { models, configOptions: options }, harnessRef.current);
				if (options.length) turnOptionsLoaded.current = true;
				return options;
			}),
		[cacheKey, cfg, offline, models, runAction, sessionId],
	);
	const reloadMcp = useCallback(
		async () => {
			await runAction("mcp", () => requireConfig(cfg, offline, (c) => reloadMcpServers(c, sessionId)));
			skillsCache.delete(cacheKey);
			setSkills([]);
		},
		[cacheKey, cfg, offline, runAction, sessionId],
	);
	const rename = useCallback(
		(title: string) => runAction("rename", () => requireConfig(cfg, offline, (c) => setConversationTitle(c, sessionId, title))),
		[cfg, offline, runAction, sessionId],
	);

	return {
		snapshot,
		loading,
		refreshing,
		loadingOlder,
		error,
		unavailable,
		models,
		configOptions,
		skills,
		pendingSends,
		completedRetry,
		pendingActions,
		actionError,
		actionErrors,
		actionCodes,
		refresh,
		loadOlder,
		loadTurnOptions,
		loadSkills,
		send,
		retrySend,
		discardSend,
		acknowledgeSend,
		steer,
		promoteQueuedTurn,
		cancelQueuedTurn,
		interrupt,
		resolveApproval: resolveApprovalAction,
		resolveInput: resolveInputAction,
		compact,
		rollback,
		editMessage,
		chooseSettings,
		setConfigOption,
		reloadMcp,
		rename,
	};
}

async function requireConfig<T>(cfg: ServerConfig | null, offline: string, action: (cfg: ServerConfig) => Promise<T>): Promise<T> {
	if (!cfg) throw new Error(offline || NOT_PAIRED_ACTION_COPY);
	return action(cfg);
}

export function clientMessageId(): string {
	return `mobile-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

function upsertPending(items: PendingSend[], next: PendingSend): PendingSend[] {
	const index = items.findIndex((item) => item.id === next.id);
	if (index < 0) return [...items, next];
	return items.map((item, at) => (at === index ? next : item));
}

function classifyConversationError(error: unknown, reviewer = false): { permanent: boolean; code?: string; message: string } {
	const code = typeof error === "object" && error !== null && "code" in error ? String(error.code ?? "") : undefined;
	return {
		permanent: conversationErrorIsPermanent(code, reviewer),
		code,
		message: conversationActionError(error),
	};
}

function conversationPageCacheKey(machine: string, scope: string): string {
	return `${machine}/${scope}`;
}
