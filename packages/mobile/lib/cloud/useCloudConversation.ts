import AsyncStorage from "@react-native-async-storage/async-storage";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { shouldPoll } from "../appStatePoll";
import type { ConversationSnapshot } from "../chat/types";
import type { CloudClient } from "./client";
import { cloudChatKey, prependOlderCloudEvents, readCloudChatEvents, readOlderCloudChatEvents, retainCloudChat, retainedCloudChat, streamedCloudChatEvents, type CloudChatHistory } from "./chatHistory";
import { appendCloudEvents, cloudConversationSnapshot, mergeCloudEvents, runningCloudTurnId } from "./conversation";
import { followCloudSessionEvents } from "./eventStream";
import { CloudError, cloudErrorMessage, isPlanRefusal, planResetCopy } from "./errors";
import { agentModelCatalog, cachedAgentCatalog, cachedSessionCatalog, modelsRetryDelay, rememberSessionCatalog, sessionCatalogKey } from "./modelCatalog";
import { cloudLifecycleStage } from "./lifecycle";
import type { CloudApprovalMode, CloudChatModel, CloudChatModels, CloudClientEvent, CloudSession } from "./types";

// Chat events arrive over the session's event stream as they are committed.
// The poll is the safety net, as on the desktop: every second while the
// stream is down or still opening (a carrier proxy can cut or hold one), and
// every ten seconds while it is up.
const EVENT_POLL_MS = 1_000;
const EVENT_SAFETY_POLL_MS = 10_000;
const SESSION_POLL_MS = 5_000;
const PRESENCE_MS = 60_000;

type ShownCatalog = { catalog: CloudChatModels; source: "session" | "agent" };

/** Agents whose worker lists chat models. */
function listsModels(harness: string | undefined): harness is "claude-code" | "codex" {
	return harness === "claude-code" || harness === "codex";
}

/** What the picker can show before asking anyone: this worker's last answer, else the agent's catalog. */
function knownCatalog(orgId: string | undefined, sessionId: string, session: CloudSession | null | undefined): ShownCatalog | undefined {
	if (!orgId || !session || !listsModels(session.harness)) return undefined;
	const worker = cachedSessionCatalog(sessionCatalogKey(orgId, sessionId, session.workerEpoch));
	if (worker) return { catalog: worker, source: "session" };
	const agent = session.isTerminated ? undefined : cachedAgentCatalog(orgId, session.harness);
	return agent ? { catalog: agent, source: "agent" } : undefined;
}

export type CloudTurnSettings = { model?: string; reasoningEffort?: string; approvalMode?: CloudApprovalMode; plan?: boolean };

/**
 * Waking a paused sandbox, kept apart from load errors: a plan refusing the
 * wake is not a connection problem, and the person needs to know when the
 * limit lifts rather than watch a spinner.
 */
export type CloudWake =
	| { phase: "idle" }
	| { phase: "waking" }
	| { phase: "refused"; message: string; reset?: string }
	| { phase: "failed"; message: string };

const settingsKey = (orgId: string, sessionId: string) => `ao.cloud.turnSettings.${orgId}.${sessionId}`;

async function readSettings(orgId: string, sessionId: string): Promise<CloudTurnSettings> {
	try {
		const raw = await AsyncStorage.getItem(settingsKey(orgId, sessionId));
		const parsed = raw ? JSON.parse(raw) as CloudTurnSettings : {};
		return typeof parsed === "object" && parsed ? parsed : {};
	} catch {
		return {};
	}
}

export type CloudConversation = {
	session: CloudSession | null;
	snapshot: ConversationSnapshot | null;
	loading: boolean;
	error: string | null;
	wake: CloudWake;
	/** Ask to wake the sandbox again (after a limit lifted, or a failed wake). */
	retryWake(): void;
	models: CloudChatModel[];
	modelsLoading: boolean;
	settings: CloudTurnSettings;
	/** Plan mode is offered by this worker (Claude Code). */
	planAvailable: boolean;
	sending: boolean;
	stopping: boolean;
	/**
	 * Appends a message. While a turn runs it waits behind it: the control plane
	 * keeps the session's messages in order, as the composer's "Send after this" says.
	 */
	send(text: string, attachmentIds?: readonly string[]): Promise<void>;
	/** Adds the text to the running turn; with no turn running it is sent as a message. */
	steer(text: string): Promise<void>;
	stop(): Promise<void>;
	decide(requestId: string, decisionId: string): Promise<void>;
	chooseSettings(settings: CloudTurnSettings): Promise<void>;
	refresh(): Promise<void>;
	retryStartup(): Promise<void>;
	/** An older page of the history is being read. */
	loadingOlder: boolean;
	/** Reads the page before the oldest loaded event (the chat opens on its newest page). */
	loadOlder(): Promise<void>;
};

/**
 * One cloud session's conversation, live while its screen is open: the session
 * row every few seconds, chat events as the session's stream delivers them
 * (with a poll behind it), a presence heartbeat so the sandbox does not
 * idle-pause under a reader, and a wake on open.
 *
 * Its history reads newest first (chatHistory.ts): the newest page, then the
 * half hour before it, and older pages on request. A chat reopened within the
 * hour shows what it held at once and reads only what is newer.
 */
export function useCloudConversation(input: { client: CloudClient; orgId: string | undefined; sessionId: string; initial?: CloudSession; active: boolean }): CloudConversation {
	const { client, orgId, sessionId, initial, active } = input;
	const [session, setSession] = useState<CloudSession | null>(initial ?? null);
	const chatKey = orgId ? cloudChatKey(client.baseUrl, orgId, sessionId) : undefined;
	const [events, setEvents] = useState<CloudClientEvent[]>(() => (chatKey && retainedCloudChat(chatKey)?.events) || []);
	const [history, setHistory] = useState<CloudChatHistory | undefined>(() => chatKey ? retainedCloudChat(chatKey)?.history : undefined);
	const [eventsLoaded, setEventsLoaded] = useState(() => Boolean(chatKey && retainedCloudChat(chatKey)));
	const [loadingOlder, setLoadingOlder] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [shown, setShown] = useState<ShownCatalog | undefined>(() => knownCatalog(orgId, sessionId, initial));
	const models: CloudChatModel[] = shown?.catalog.models ?? [];
	const modes: string[] = shown?.catalog.modes ?? [];
	const [modelsLoading, setModelsLoading] = useState(false);
	const [settings, setSettings] = useState<CloudTurnSettings>({});
	const [sending, setSending] = useState(false);
	const [stopping, setStopping] = useState(false);
	const [wake, setWake] = useState<CloudWake>({ phase: "idle" });
	const [wakeAttempt, setWakeAttempt] = useState(0);
	const eventsRef = useRef<CloudClientEvent[]>([]);
	eventsRef.current = events;
	const historyRef = useRef(history);
	historyRef.current = history;
	const loadingEvents = useRef(false);
	const loadingOlderRef = useRef(false);
	// Changes when the list starts over (another session, or the newest page
	// again), so a read made for the earlier list is dropped.
	const generation = useRef(0);
	const [streaming, setStreaming] = useState(false);
	const streamingRef = useRef(false);
	streamingRef.current = streaming;
	const lastPoll = useRef(0);
	const [foreground, setForeground] = useState(() => shouldPoll(AppState.currentState));
	const resumed = useRef(false);

	/** Holds the list and what it lacks, and keeps them for a reopen. */
	const hold = useCallback((key: string | undefined, nextEvents: CloudClientEvent[], nextHistory: CloudChatHistory | undefined) => {
		eventsRef.current = nextEvents;
		historyRef.current = nextHistory;
		setEvents(nextEvents);
		setHistory(nextHistory);
		if (key) retainCloudChat(key, nextEvents, nextHistory);
	}, []);

	useEffect(() => {
		const held = chatKey ? retainedCloudChat(chatKey) : undefined;
		generation.current++;
		eventsRef.current = held?.events ?? [];
		historyRef.current = held?.history;
		setSession(initial ?? null);
		setEvents(held?.events ?? []);
		setHistory(held?.history);
		setEventsLoaded(Boolean(held));
		setError(null);
		setWake({ phase: "idle" });
		resumed.current = false;
		// Only the identity of the session resets the view.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [orgId, sessionId]);

	useEffect(() => {
		if (!orgId) return;
		let cancelled = false;
		void readSettings(orgId, sessionId).then((value) => { if (!cancelled) setSettings(value); });
		return () => { cancelled = true; };
	}, [orgId, sessionId]);

	const loadSession = useCallback(async () => {
		if (!orgId) return;
		try {
			const { session: next } = await client.getSession(orgId, sessionId);
			setSession(next);
			setError(null);
		} catch (cause) {
			setError(cloudErrorMessage(cause));
		}
	}, [client, orgId, sessionId]);

	const loadEvents = useCallback(async () => {
		if (!orgId || loadingEvents.current) return;
		loadingEvents.current = true;
		lastPoll.current = Date.now();
		const own = generation.current;
		try {
			const read = await readCloudChatEvents(client, orgId, sessionId, eventsRef.current);
			if (own !== generation.current) return;
			if (read.startOver) {
				// A new list: an older read in flight was for the one it replaces. The
				// stream may have added events while this read ran; those after it stay.
				generation.current++;
				hold(chatKey, appendCloudEvents(read.events, eventsRef.current), read.startOver.history);
			} else {
				// The stream may have added events while this page loaded.
				const merged = mergeCloudEvents(eventsRef.current, read.events);
				if (merged !== eventsRef.current) hold(chatKey, merged, historyRef.current);
			}
			setEventsLoaded(true);
		} catch (cause) {
			setError(cloudErrorMessage(cause));
		} finally {
			loadingEvents.current = false;
		}
	}, [chatKey, client, hold, orgId, sessionId]);

	const loadOlder = useCallback(async (recent = false) => {
		const held = historyRef.current;
		if (!orgId || !held || loadingOlderRef.current) return;
		if (!held.hasOlder) {
			if (held.recentPending) hold(chatKey, eventsRef.current, { ...held, recentPending: false });
			return;
		}
		const own = generation.current;
		loadingOlderRef.current = true;
		setLoadingOlder(true);
		if (recent) hold(chatKey, eventsRef.current, { ...held, recentPending: false });
		try {
			const read = await readOlderCloudChatEvents(client, orgId, sessionId, eventsRef.current, held, { recent });
			if (own !== generation.current) return;
			if (read.restart) {
				// The control plane stopped reading back (a rollback): the whole history, forward.
				const whole = await readCloudChatEvents(client, orgId, sessionId, []);
				if (own !== generation.current) return;
				generation.current++;
				hold(chatKey, appendCloudEvents(whole.events, eventsRef.current), whole.startOver?.history);
				return;
			}
			hold(chatKey, prependOlderCloudEvents(eventsRef.current, read.older), read.history);
		} catch {
			// The control stays: another press reads the page again.
		} finally {
			loadingOlderRef.current = false;
			setLoadingOlder(false);
		}
	}, [chatKey, client, hold, orgId, sessionId]);

	// The half hour before the newest page, read right after it, so the chat
	// opens on the recent exchange without reading the whole history.
	const recentPending = Boolean(eventsLoaded && history?.recentPending);
	useEffect(() => {
		if (active && recentPending) void loadOlder(true);
	}, [active, loadOlder, recentPending]);

	// Live polling while the screen is in front.
	useEffect(() => {
		if (!active || !orgId) return;
		void loadSession();
		void loadEvents();
		const events = setInterval(() => {
			if (!shouldPoll(AppState.currentState)) return;
			if (streamingRef.current && Date.now() - lastPoll.current < EVENT_SAFETY_POLL_MS) return;
			void loadEvents();
		}, EVENT_POLL_MS);
		const rows = setInterval(() => { if (shouldPoll(AppState.currentState)) void loadSession(); }, SESSION_POLL_MS);
		const presence = setInterval(() => {
			if (shouldPoll(AppState.currentState)) void client.touchPresence(orgId, sessionId).catch(() => {});
		}, PRESENCE_MS);
		void client.touchPresence(orgId, sessionId).catch(() => {});
		return () => { clearInterval(events); clearInterval(rows); clearInterval(presence); };
	}, [active, client, loadEvents, loadSession, orgId, sessionId]);

	useEffect(() => {
		const subscription = AppState.addEventListener("change", (state) => setForeground(shouldPoll(state)));
		return () => subscription.remove();
	}, []);

	// The live stream, once the first poll has loaded the history it resumes
	// after; it closes while the app is in the background or the screen is covered.
	useEffect(() => {
		if (!active || !orgId || !foreground || !eventsLoaded) return;
		const stop = followCloudSessionEvents({
			client, orgId, sessionId,
			resumeAfter: () => eventsRef.current.at(-1)?.sequence ?? 0,
			onEvents: (incoming) => {
				// None below a list that lacks older history: the hole they would leave is never read.
				const merged = mergeCloudEvents(eventsRef.current, streamedCloudChatEvents(eventsRef.current, historyRef.current, incoming));
				if (merged === eventsRef.current) return;
				hold(chatKey, merged, historyRef.current);
			},
			onOpenChange: setStreaming,
		});
		return () => {
			stop();
			setStreaming(false);
		};
	}, [active, chatKey, client, eventsLoaded, foreground, hold, orgId, sessionId]);

	// Opening a paused session wakes it, once per visit (the desktop does the same).
	useEffect(() => {
		if (!active || !orgId || !session || resumed.current || session.isTerminated) return;
		const stage = cloudLifecycleStage(session);
		if (stage !== "paused") return;
		resumed.current = true;
		setWake({ phase: "waking" });
		void client.resumeSession(orgId, sessionId).then(() => {
			setWake({ phase: "idle" });
			return loadSession();
		}).catch((cause) => {
			if (cause instanceof CloudError && cause.code === "SESSION_TERMINATED") setWake({ phase: "idle" });
			else if (isPlanRefusal(cause)) setWake({ phase: "refused", message: cause.message || "Your plan's usage limit is reached.", reset: planResetCopy(cause.resetsAt) });
			else setWake({ phase: "failed", message: cloudErrorMessage(cause) });
		});
		// wakeAttempt re-runs this after an explicit retry.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [active, client, loadSession, orgId, session, sessionId, wakeAttempt]);

	// A sandbox that came back (the limit lifted, or a wake from elsewhere) clears the notice.
	const connectedNow = Boolean(session && cloudLifecycleStage(session) === "connected");
	useEffect(() => {
		if (connectedNow) setWake((current) => (current.phase === "idle" ? current : { phase: "idle" }));
	}, [connectedNow]);

	const retryWake = useCallback(() => {
		resumed.current = false;
		setWakeAttempt((attempt) => attempt + 1);
	}, []);

	// The model catalog is the live worker's, but the worker has to be awake and
	// start the agent to answer, which takes seconds (more after a wake). Until
	// it does, the picker shows the control plane's catalog for the agent, which
	// needs no worker, and both are remembered for the next open. As the desktop
	// does (CloudSessionChatSurface).
	const harness = session?.harness;
	const connected = Boolean(session?.runtimeConnected);
	const terminated = Boolean(session?.isTerminated);
	const catalogKey = orgId && session && listsModels(harness) ? sessionCatalogKey(orgId, sessionId, session.workerEpoch) : undefined;
	const shownSource = useRef(shown?.source);
	shownSource.current = shown?.source;
	useEffect(() => {
		if (!orgId || !catalogKey || !listsModels(harness)) return;
		const worker = cachedSessionCatalog(catalogKey);
		if (worker) { setShown({ catalog: worker, source: "session" }); return; }
		// A new worker's catalog is asked again; until then, the agent's.
		if (shownSource.current === "session") setShown(undefined);
		if (terminated) return;
		let cancelled = false;
		void agentModelCatalog(client, orgId, harness)
			.then((catalog) => { if (!cancelled && shownSource.current !== "session") setShown({ catalog, source: "agent" }); })
			.catch(() => {});
		return () => { cancelled = true; };
	}, [catalogKey, client, harness, orgId, terminated]);

	useEffect(() => {
		if (!active || !orgId || !catalogKey || !connected || terminated) return;
		let cancelled = false;
		setModelsLoading(true);
		void (async () => {
			for (let failures = 0; !cancelled;) {
				try {
					const catalog = await client.listChatModels(orgId, sessionId);
					if (cancelled) return;
					rememberSessionCatalog(catalogKey, catalog);
					setShown({ catalog, source: "session" });
					return;
				} catch (cause) {
					failures += 1;
					// A worker still connecting is asked again within a second or two,
					// not after a fixed five.
					const delay = modelsRetryDelay(failures, cause);
					if (delay === undefined || cancelled) return;
					await new Promise((resolve) => setTimeout(resolve, delay));
				}
			}
		})().finally(() => { if (!cancelled) setModelsLoading(false); });
		return () => {
			cancelled = true;
			setModelsLoading(false);
		};
	}, [active, catalogKey, client, connected, orgId, sessionId, terminated]);

	const snapshot = useMemo(() => {
		if (!session || !eventsLoaded) return null;
		return cloudConversationSnapshot(session, events, {
			settings: { model: settings.model ?? session.model, reasoningEffort: settings.reasoningEffort, approvalMode: settings.approvalMode },
			partial: Boolean(history?.hasOlder),
		});
	}, [events, eventsLoaded, history?.hasOlder, session, settings]);

	const send = useCallback(async (text: string, attachmentIds: readonly string[] = []) => {
		const body = text.trim();
		if (!orgId || !session || (!body && !attachmentIds.length)) return;
		setSending(true);
		try {
			await client.sendMessage(orgId, sessionId, {
				text: body,
				...(attachmentIds.length ? { attachmentIds: [...attachmentIds] } : {}),
				...(settings.model ? { model: settings.model } : {}),
				...(settings.reasoningEffort ? { reasoningEffort: settings.reasoningEffort } : {}),
				// Plan mode is a read-only turn; the session's own mode applies otherwise.
				...(settings.plan ? { mode: "read-only" as const } : {}),
				...(settings.approvalMode ? { approvalMode: settings.approvalMode } : {}),
			});
			await loadEvents();
		} catch (cause) {
			// A message to a paused session wakes it, so it meets the same plan limit.
			if (isPlanRefusal(cause)) setWake({ phase: "refused", message: cause.message || "Your plan's usage limit is reached.", reset: planResetCopy(cause.resetsAt) });
			throw cause;
		} finally {
			setSending(false);
		}
	}, [client, loadEvents, orgId, session, sessionId, settings, snapshot]);

	const steer = useCallback(async (text: string) => {
		const body = text.trim();
		const running = snapshot ? runningCloudTurnId(snapshot) : undefined;
		if (!orgId || !body) return;
		// The turn ended while the steer was being typed: it becomes the next message.
		if (!running || !snapshot?.capabilities?.includes("steer")) return send(body);
		setSending(true);
		try {
			await client.steerTurn(orgId, sessionId, running, body);
			await loadEvents();
		} finally {
			setSending(false);
		}
	}, [client, loadEvents, orgId, send, sessionId, snapshot]);

	const stop = useCallback(async () => {
		const running = snapshot ? runningCloudTurnId(snapshot) : undefined;
		if (!orgId || !running) return;
		setStopping(true);
		try {
			await client.cancelTurn(orgId, sessionId, running);
			await loadEvents();
		} finally {
			setStopping(false);
		}
	}, [client, loadEvents, orgId, sessionId, snapshot]);

	const decide = useCallback(async (requestId: string, decisionId: string) => {
		if (!orgId) return;
		await client.decideApproval(orgId, sessionId, requestId, decisionId);
		await loadEvents();
	}, [client, loadEvents, orgId, sessionId]);

	const chooseSettings = useCallback(async (next: CloudTurnSettings) => {
		if (!orgId) return;
		const merged = { ...settings, ...next };
		setSettings(merged);
		await AsyncStorage.setItem(settingsKey(orgId, sessionId), JSON.stringify(merged)).catch(() => {});
	}, [orgId, sessionId, settings]);

	const loadOlderPage = useCallback(() => loadOlder(), [loadOlder]);

	const refresh = useCallback(async () => {
		await Promise.all([loadSession(), loadEvents()]);
	}, [loadEvents, loadSession]);

	const retryStartup = useCallback(async () => {
		if (!orgId) return;
		const { session: next } = await client.retryStartup(orgId, sessionId);
		setSession(next);
	}, [client, orgId, sessionId]);

	return {
		session,
		snapshot,
		loading: !snapshot && !error,
		error,
		wake,
		retryWake,
		models,
		modelsLoading,
		settings,
		planAvailable: harness === "claude-code" && modes.includes("plan"),
		sending,
		stopping,
		send,
		steer,
		stop,
		decide,
		chooseSettings,
		refresh,
		retryStartup,
		loadingOlder,
		loadOlder: loadOlderPage,
	};
}
