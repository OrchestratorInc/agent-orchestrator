import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { File } from "expo-file-system";
import { attachmentsKey, clearStartedSpawnDraft, createSpawnDraftSaver, loadSpawnDraft, requestMatches, saveBeforeStart, startRefused, type SpawnDraft, type SpawnDraftRequest, type SpawnDraftSelections } from "../lib/spawnDraft";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState, Keyboard, Pressable, StyleSheet, Text, View } from "react-native";
import Animated, { useAnimatedStyle } from "react-native-reanimated";
import { KeyboardAvoidingView, useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { agentErrorCopy } from "../lib/agentError";
import { AppSymbol } from "../lib/AppSymbol";
import { defaultAgent, rankAgents } from "../lib/agentPicker";
import { ApiError, getAgentModels, getAgents, getProject, getSettings, type AgentCatalog, type AgentModelCatalog, type AgentModelInfo, type ProjectDetail, type SessionMode } from "../lib/api";
import type { ServerConfig } from "../lib/config";
import { userFacingError } from "../lib/connectionError";
import { chatErrorCopy, isChatPreflightError } from "../lib/chatError";
import { haptics } from "../lib/haptics";
import { autoSpawnHostId, connectedSpawnHosts, selectSpawnHost, spawnHostNeedsRetry, spawnHostRequestIsCurrent } from "../lib/hostRoute";
import { rememberLastProject, useLastProject } from "../lib/lastProject";
import { ALL_PROJECTS, STANDALONE_PROJECT, resolveSpawnProject } from "../lib/projectFilter";
import { claudeAliasModel, defaultEffortModel, defaultRowModelToSend, menuModelName, menuModels, modelOverride, resolveSpawnAgent, resolveSpawnModel, spawnModelSourceChanged } from "../lib/spawnModel";
import { appendSpawnAttachments, joinAttachmentErrors, MAX_SPAWN_ATTACHMENTS, pastedSpawnAttachments, photoPickerAttachment, readSpawnAttachments, spawnAttachmentPreview, type PickedSpawnAttachment, type SpawnAttachment } from "../lib/spawn-attachments";
import { ComposerAttachmentTray } from "../lib/chat/ComposerAttachmentTray";
import type { ComposerImageAsset, PasteOverflow } from "../lib/chat/composerImages";
import { SpawnComposerControls } from "../lib/spawn-composer-controls";
import { SpawnSuggestionHost, type SpawnSuggestionOverlay } from "../lib/spawn-composer";
import { errorAfterEdit, spawnHostState, spawnNotices, START_DROPPED_NOTICE } from "../lib/spawnNotices";
import { createAgentModelCache, nextConnectionEpoch, provenSpawnConfig, type ConnectionEpoch } from "../lib/spawnLoads";
import { loadSpawnSkills } from "../lib/spawnSkills";
import { spawnEffortChoices, spawnEffortToSend } from "../lib/modelEffort";
import { useApp } from "../lib/store";
import { useCloud } from "../lib/cloud/CloudProvider";
import { connectedAgents } from "../lib/cloud/agents";
import { CLOUD_HOST_ID, CLOUD_HOST_NAME, isCloudHost } from "../lib/cloud/mapping";
import { cloudPlanBlock, cloudSessionBilled, planBlocksCloudSetup } from "../lib/cloud/plan";
import { useCloudPlan } from "../lib/cloud/planStore";
import { agentModelCatalog, isReleaseCatalog } from "../lib/cloud/modelCatalog";
import { useReleaseCatalogRereads } from "../lib/cloud/useReleaseCatalogRereads";
import { cloudAgentCatalog, cloudModelCatalog, cloudProjectDetail, cloudSpawnProjectOptions, NEW_CLOUD_PROJECT } from "../lib/cloud/spawnSource";
import { standaloneAgentsOffered } from "../lib/cloud/standaloneAgents";
import { useVoiceInput } from "../lib/voice/useVoiceInput";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { Button } from "../lib/ui";
import { iconSize, radius, space, type } from "../lib/tokens";
import { approvalOptions, isApprovalMode, resolveApprovalMode, type ApprovalMode } from "../lib/spawnApproval";
import { modelSheetRoute } from "../lib/sheetResult";
import { agentAccountLabel, useAgentAccounts } from "../lib/agentAccounts";
import { useChatKeyboard } from "../lib/chat/useChatKeyboard";
import { SYSTEM_GLASS_HEADER } from "../lib/stackScreenOptions";
import { PillHeaderFrame } from "../lib/headerPill";
import { useGlassBarInset } from "../lib/useHeaderOverlap";
import { KEYBOARD_DOCK_GAP } from "../lib/session/keyboardInset";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

// Android: the chat's pill bar (see `pillHeaderOptions` on the route) over its
// frost; the page holds itself below the bar through `useGlassBarInset`.
export default function SpawnScreen() {
	return (
		<PillHeaderFrame>
			<SpawnPage />
		</PillHeaderFrame>
	);
}

function SpawnPage() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const navigation = useNavigation();
	const { projectId: routeProjectId, hostId: routeHostId } = useLocalSearchParams<{ projectId?: string; hostId?: string }>();
	const { hostStates, cloud, activeProjectId, currentHostId, configForHost, switchHost, setActiveProject, spawn, configResolved, phoneNetwork, refreshHost } = useApp();
	const cloudState = useCloud();
	// Orchestrator Cloud is the picker's last choice, after the connected machines, while signed in.
	const machineHosts = useMemo(() => connectedSpawnHosts(hostStates), [hostStates]);
	const connectedHosts = useMemo(() => cloud ? [...machineHosts, { id: CLOUD_HOST_ID, label: CLOUD_HOST_NAME, icon: "cloud" as const }] : machineHosts, [cloud, machineHosts]);
	// Whether Cloud is offered, or known not to be: signed in and loaded, signed
	// out, or failed. A Cloud draft's page waits for it before picking.
	const cloudResolved = Boolean(cloud) || (cloudState.status !== "loading" && cloudState.status !== "connecting");
	const [selectedHostId, setSelectedHostId] = useState<string | null>(null);
	// The machine the page last asked for. Until a render shows it, effects later
	// in the same commit still see the machine being left (see the project seed).
	const selectionTarget = useRef<string | null>(null);
	const selectHost = (hostId: string | null) => {
		selectionTarget.current = hostId;
		setSelectedHostId(hostId);
	};
	const hostSelectionInitialized = useRef(false);
	// Set once a machine is picked by hand, or the one chosen for the person can
	// be used: until then the page keeps choosing (see autoSpawnHostId).
	const hostSettled = useRef(false);
	// The person has edited the form (any control, typing or dictation). From
	// then on the chosen machine stays: a move would clear their choices and
	// send Start to a machine they never picked.
	const formEdited = useRef(false);
	// State, not a ref: what was typed while the saved New task draft was being
	// read is saved as soon as the read is done (see the draft below).
	const [draftRestored, setDraftRestored] = useState(false);
	// The machine the restored draft was written for, picked again if it is connected.
	const restoredHostId = useRef<string | null>(null);
	const catalogGeneration = useRef(0);
	const selectedHost = hostStates.find((host) => host.hostId === selectedHostId);
	const cloudSelected = isCloudHost(selectedHostId);
	const liveConfig = selectedHostId && !cloudSelected ? configForHost(selectedHostId) : null;
	// A machine can be chosen while it reconnects, so the page's own requests
	// wait for a connection the machine has answered a poll on (provenSpawnConfig).
	const provenConfig = useRef<ServerConfig | null>(null);
	const config = provenSpawnConfig(provenConfig.current, liveConfig, selectedHost?.connection ?? "closed");
	if (config) provenConfig.current = config;
	// The pickers load once per machine, not once per connection. An app switch
	// drops the connection and brings it back, and the loads below used to empty
	// every picker and fetch it all again each time. Each remembers what it last
	// loaded (`loadedFor`) and keeps it on screen while the connection is away;
	// a load whose inputs changed meanwhile runs once it is back. Requests only
	// ever go through the live `config`. A connection that really changed
	// (another endpoint or credential, as an upgrade race brings) moves `epoch`
	// on, which loads them all again through it.
	const connected = Boolean(config);
	const connectionEpoch = useRef<ConnectionEpoch>({ last: null, epoch: 0 });
	connectionEpoch.current = nextConnectionEpoch(connectionEpoch.current, config);
	const epoch = connectionEpoch.current.epoch;
	const loadedFor = useRef<{ catalog?: string; project?: string; models?: string }>({});
	// The agent menu's lists, kept per agent: one that failed is asked again on
	// the next load without asking again for the ones that loaded.
	const agentModelCache = useRef(createAgentModelCache());
	const projects = cloudSelected ? cloud?.projects ?? [] : selectedHost?.projects ?? [];
	// Cloud's list is known once it loaded, not as soon as Cloud is signed in:
	// before that, an empty list would ask to create a project that exists.
	const projectsKnown = cloudSelected ? Boolean(cloud) && cloudState.projectsLoaded : selectedHost?.projectsKnown ?? false;
	const connection = cloudSelected ? (cloud ? "open" : "closed") : selectedHost?.connection ?? "closed";
	// Cloud starts a standalone agent (no project) where its control plane offers them.
	const cloudStandaloneOffered = cloudSelected && standaloneAgentsOffered(cloudState.org?.offering ?? cloudState.me?.offering);
	const cloudAgentsKey = connectedAgents(cloudState.connections).join(",");
	// Known before anything is typed: a Free organization runs no cloud agents,
	// and a spent session-hour window starts none until it resets. The control
	// plane still decides on send; this only spares writing a task it would refuse.
	const cloudPlan = useCloudPlan(cloudState.client, cloud ? cloudState.org?.id : undefined);
	const reloadCloudPlan = cloudPlan?.reload;
	useEffect(() => {
		if (cloudSelected) void reloadCloudPlan?.();
	}, [cloudSelected, reloadCloudPlan]);
	// Offline or rejected by the same rule as the other screens: a connection the
	// app closed on going to the background is neither (see spawnHostState).
	const machine = spawnHostState(selectedHost, phoneNetwork, Date.now());
	const hostOptions = useMemo(() => {
		if (!selectedHostId || connectedHosts.some((host) => host.id === selectedHostId)) return connectedHosts;
		const selected = hostStates.find((host) => host.hostId === selectedHostId);
		return selected ? [{ id: selected.hostId, label: selected.name }, ...connectedHosts] : connectedHosts;
	}, [connectedHosts, hostStates, selectedHostId]);

	// Back from the background every machine is reconnecting, and a choice made
	// once then stuck: no machine, another machine or Cloud. The page keeps
	// choosing until a machine is picked or the chosen one can be used, so the
	// default (or the route's, or the draft's) shows at once and is ready as
	// soon as it answers.
	useEffect(() => {
		if (hostSettled.current) return;
		// Read here, not where the edit happened, so an edit made before any
		// machine was chosen never leaves the page on none.
		if (formEdited.current && selectedHostId !== null) {
			hostSettled.current = true;
			return;
		}
		// The saved draft is read first: restored, it goes back to its own machine.
		if (!draftRestored) return;
		const now = Date.now();
		const auto = autoSpawnHostId({
			routeHostId,
			draftHostId: restoredHostId.current ?? undefined,
			currentHostId: currentHostId ?? undefined,
			hosts: hostStates.map((host) => ({ hostId: host.hostId, ready: machineHosts.some((item) => item.id === host.hostId), state: spawnHostState(host, phoneNetwork, now) })),
			cloud: Boolean(cloud),
			configResolved,
			cloudResolved,
		});
		if (!auto.settled) return;
		hostSelectionInitialized.current = true;
		if (auto.hostId !== selectedHostId) {
			if (selectedHostId !== null) resetHostChoice();
			selectHost(auto.hostId);
		}
		// Usable now: kept from here on, so a later blip never moves the page.
		if (isCloudHost(auto.hostId) || machineHosts.some((host) => host.id === auto.hostId)) hostSettled.current = true;
		// resetHostChoice only clears state; it changes with every render.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [cloud, cloudResolved, configResolved, currentHostId, draftRestored, hostStates, machineHosts, phoneNetwork, routeHostId, selectedHostId]);

	// Opened on a machine whose last try failed, ask again now instead of at the
	// next recovery try: the person is waiting to use it. Once per machine; the
	// store's own retries take over after that.
	const retriedHost = useRef<string | null>(null);
	useEffect(() => {
		if (!selectedHostId || cloudSelected || retriedHost.current === selectedHostId) return;
		if (!spawnHostNeedsRetry(selectedHost && { connection: selectedHost.connection, loading: selectedHost.loading, config: liveConfig })) return;
		retriedHost.current = selectedHostId;
		void refreshHost(selectedHostId).catch(() => {});
	}, [cloudSelected, liveConfig, refreshHost, selectedHost, selectedHostId]);

	const [projectId, setProjectId] = useState<string | null>(null);
	// Only a session the plan pays for is the plan's to stop: Coder the
	// organization runs free still starts on Free.
	const cloudProjectProvider = cloudState.projects.find((item) => item.id === projectId)?.config.sandboxProvider;
	const planBlock = cloudSelected ? cloudPlanBlock(cloudPlan?.summary, Date.now(), cloudSessionBilled(cloudProjectProvider, cloudState.org?.offering)) : undefined;
	const [harness, setHarness] = useState("");
	const [agentTouched, setAgentTouched] = useState(false);
	const [mode, setMode] = useState<SessionMode>("chat");
	const [chatHarnesses, setChatHarnesses] = useState<string[]>([]);
	const [prompt, setPrompt] = useState("");
	const [attachments, setAttachments] = useState<SpawnAttachment[]>([]);
	const attachmentsRef = useRef<SpawnAttachment[]>([]);
	// The prompt and attachments are saved as they change, so they survive the
	// phone closing the app in the background (see spawnDraft). A draft from
	// before comes back when the page opens empty; starting the task clears it.
	// It is saved for the machine picked and the account signed in, so another
	// account, or the same phone after that machine is forgotten, never finds it.
	const [draftSaver] = useState(() => createSpawnDraftSaver());
	// Bumped by each read of the saved draft, and on leaving the page: only the latest one lands.
	const restoreGeneration = useRef(0);
	// The last read of the saved draft failed: it may still be on the phone, so
	// an empty composer is not saved over it (which would delete it).
	const draftUnread = useRef(false);
	// The saved draft is another machine's than the one this page was opened
	// for: an empty composer is not saved over it either.
	const draftElsewhere = useRef(false);
	// The draft a Start began a task with: cleared rather than saved again, until
	// the prompt or attachments change (on a page kept open after it started).
	const startedDraft = useRef<{ prompt: string; attachments: readonly SpawnAttachment[] } | null>(null);
	const promptRef = useRef(prompt);
	promptRef.current = prompt;
	// Until Cloud has read who is signed in, a draft is neither saved (it would
	// be saved as no account's, which any account then finds) nor cleared.
	const accountKnown = cloudState.status !== "loading";
	const draftAccountId = cloudState.account?.id ?? null;
	const draftAccountRef = useRef(draftAccountId);
	draftAccountRef.current = draftAccountId;
	const draftOwner = useMemo(() => ({ hostId: selectedHostId, accountId: draftAccountId }), [draftAccountId, selectedHostId]);
	const pickingAttachments = useRef(false);
	// A paste that came in while a pick was loading, said again when it ends.
	const missedPaste = useRef(false);
	const [attachmentError, setAttachmentError] = useState<string>();
	// Keyed by place and name: picked files carry no id of their own, and the
	// same file can be picked twice.
	const trayItems = useMemo(() => attachments.map((item, index) => ({ key: `${index}-${item.name}`, name: item.name, previewUri: spawnAttachmentPreview(item) })), [attachments]);
	const [model, setModel] = useState("");
	const [modelTouched, setModelTouched] = useState(false);
	// An effort picked here; until then the model runs at its own default.
	const [effort, setEffort] = useState<string>();
	const [modelCatalog, setModelCatalog] = useState<AgentModelCatalog>();
	// Every agent's own models, so the agent menu can offer each one's list
	// before it is picked. The picked agent's list is the project-aware catalog.
	const [agentModelLists, setAgentModelLists] = useState<Record<string, AgentModelInfo[]>>({});
	const [approvalChoice, setApprovalChoice] = useState<ApprovalMode>();
	const [accountPick, setAccountPick] = useState<string>();
	const spawnAccounts = useAgentAccounts(config, cloudSelected ? undefined : harness);
	// The agent account a restored draft's Start used, put back once the page is
	// on the draft's machine and agent: each of those, picked as the page sets
	// up, starts the account over below. Kept with the draft until then.
	const [restoredAccount, setRestoredAccount] = useState<{ hostId: string | null; harness: string; accountId: string } | null>(null);
	const accountScope = useRef<string | null>(null);
	useEffect(() => {
		const scope = JSON.stringify([selectedHostId, harness]);
		const moved = accountScope.current !== scope;
		accountScope.current = scope;
		if (restoredAccount && selectedHostId === restoredAccount.hostId && harness === restoredAccount.harness) {
			setAccountPick(restoredAccount.accountId);
			setRestoredAccount(null);
			return;
		}
		if (moved) setAccountPick(undefined);
		// Still setting up (no machine, or the draft's machine with no agent yet)
		// keeps it waiting; anything else is another choice, which drops it.
		const settingUp = restoredAccount && (selectedHostId === null || (selectedHostId === restoredAccount.hostId && !harness));
		if (restoredAccount && !settingUp) setRestoredAccount(null);
	}, [restoredAccount, selectedHostId, harness]);
	const accountOptions = useMemo(() => spawnAccounts.accounts.filter((account) => account.signedIn).map((account) => ({ id: account.id, label: agentAccountLabel(account, spawnAccounts.accounts) })), [spawnAccounts.accounts]);
	const shownAccountId = accountPick || spawnAccounts.accounts.find((account) => account.default)?.id;
	const [projectDetail, setProjectDetail] = useState<ProjectDetail>();
	const [projectDetailLoadedFor, setProjectDetailLoadedFor] = useState<string | null>(null);
	const [modelLoading, setModelLoading] = useState(false);
	const [modelError, setModelError] = useState<string>();
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const requestRef = useRef<SpawnDraftRequest | undefined>(undefined);
	// What goes into a Start besides the prompt, saved with the draft so a
	// restored one sends what it sent before (and so reuses its request id).
	const draftSelections = useMemo((): SpawnDraftSelections => ({
		...(projectId ? { projectId } : {}),
		...(agentTouched && harness ? { harness } : {}),
		...(modelTouched && model ? { model } : {}),
		...(effort ? { effort } : {}),
		...(approvalChoice ? { approvalMode: approvalChoice } : {}),
		mode,
		// Only an account picked (or one restored, not yet put back): the default goes unnamed, as in a Start.
		...(accountPick ? { accountId: accountPick, accountHarness: harness } : restoredAccount ? { accountId: restoredAccount.accountId, accountHarness: restoredAccount.harness } : {}),
	}), [accountPick, agentTouched, approvalChoice, effort, harness, mode, model, modelTouched, projectId, restoredAccount]);
	useEffect(() => {
		if (!draftRestored || !accountKnown) return;
		if ((draftUnread.current || draftElsewhere.current) && !prompt.trim() && attachments.length === 0) return;
		// Written here, this page's draft replaces the other machine's.
		draftElsewhere.current = false;
		const started = startedDraft.current;
		if (started && started.prompt === prompt && started.attachments === attachments) return;
		startedDraft.current = null;
		// The last Start goes with the draft only while it sent these attachments:
		// restored with others, its id must not be sent with them.
		const request = requestRef.current;
		const current = request && request.attachments === attachmentsKey(attachments) ? request : undefined;
		// Until the machine is picked, the restored draft keeps the machine it was
		// written for rather than being saved as no machine's.
		const owner = selectedHostId === null && !hostSelectionInitialized.current
			? { ...draftOwner, hostId: restoredHostId.current }
			: draftOwner;
		draftSaver.schedule({ prompt, attachments, owner, selections: draftSelections, ...(current ? { request: current } : {}) });
	}, [accountKnown, attachments, draftOwner, draftRestored, draftSaver, draftSelections, prompt, selectedHostId]);
	// Puts back what a restored draft had picked. The machine is picked once the
	// list of machines is known (above); a model or agent picked stays picked.
	const restoreSelections = (draft: SpawnDraft) => {
		restoredHostId.current = draft.owner?.hostId ?? null;
		const picked = draft.selections;
		if (!picked) return;
		if (picked.mode === "chat" || picked.mode === "tui") setMode(picked.mode);
		if (picked.projectId) setProjectId(picked.projectId);
		if (picked.harness) { setHarness(picked.harness); setAgentTouched(true); }
		if (picked.model) { setModel(picked.model); setModelTouched(true); }
		if (picked.effort) setEffort(picked.effort);
		if (isApprovalMode(picked.approvalMode)) setApprovalChoice(picked.approvalMode);
		if (picked.accountId && picked.accountHarness) setRestoredAccount({ hostId: restoredHostId.current, harness: picked.accountHarness, accountId: picked.accountId });
	};
	const restoreDraft = () => {
		const generation = ++restoreGeneration.current;
		setDraftRestored(false);
		void loadSpawnDraft().then((draft) => {
			if (generation !== restoreGeneration.current) return;
			draftUnread.current = false;
			draftElsewhere.current = false;
			if (!draft) return;
			// Routed to another machine than the draft's: the draft stays its
			// machine's, neither shown here nor saved over by this empty page. An
			// unrouted page goes to the draft's machine with it.
			const draftHostId = draft.owner?.hostId ?? null;
			if (routeHostId && draftHostId !== null && draftHostId !== routeHostId) {
				draftElsewhere.current = true;
				restoredHostId.current = null;
				return;
			}
			// Text typed or files added before the draft was read are the page's
			// now: the old draft neither replaces nor joins them.
			if (promptRef.current || attachmentsRef.current.length) return;
			// What the person chose for the draft's machine: the page keeps that
			// machine, as after an edit, so a move never clears or overwrites it.
			formEdited.current = true;
			setPrompt(draft.prompt);
			attachmentsRef.current = draft.attachments;
			setAttachments(draft.attachments);
			restoreSelections(draft);
			// The Start it last sent: sent again unchanged, it reuses the request id,
			// so a task started before the app was closed is not started twice.
			if (draft.request) requestRef.current = draft.request;
		}, () => {
			if (generation === restoreGeneration.current) draftUnread.current = true;
		}).finally(() => { if (generation === restoreGeneration.current) setDraftRestored(true); });
	};
	useEffect(() => {
		restoreDraft();
		const subscription = AppState.addEventListener("change", (state) => { if (state !== "active") void draftSaver.flush(); });
		return () => {
			restoreGeneration.current += 1;
			subscription.remove();
			void draftSaver.flush();
			draftSaver.release();
		};
		// Once, when the page opens.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [draftSaver]);
	// Another account signed in, or this one signed out, with the page open:
	// what the last account wrote leaves the composer, unsaved, and only the new
	// account's own draft can come back.
	const draftAccountSeen = useRef<string | null | undefined>(undefined);
	useEffect(() => {
		if (!accountKnown) return;
		const previous = draftAccountSeen.current;
		draftAccountSeen.current = draftAccountId;
		if (previous === undefined || previous === draftAccountId) return;
		void draftSaver.cancel();
		promptRef.current = "";
		setPrompt("");
		attachmentsRef.current = [];
		setAttachments([]);
		startedDraft.current = null;
		requestRef.current = undefined;
		setRestoredAccount(null);
		restoreDraft();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [accountKnown, draftAccountId]);

	const [catalog, setCatalog] = useState<AgentCatalog | null>(null);
	const [catalogError, setCatalogError] = useState<string | null>(null);
	const [loading, setLoading] = useState(true);
	const [offerTUI, setOfferTUI] = useState(false);
	// Bumped to re-run the loads below after the desktop comes back.
	const [reloadKey, setReloadKey] = useState(0);
	// Spoken text lands in the prompt the way it does in the chat composer:
	// appended, so dictation can extend what was typed rather than replace it.
	// Dictation edits the form too, so it clears a dropped Start's notice.
	const voice = useVoiceInput({ onTranscript: useCallback((spoken: string) => {
		setError(errorAfterEdit);
		formEdited.current = true;
		setPrompt((old) => old ? `${old} ${spoken}` : spoken);
	}, []) });
	const listening = voice.state === "starting" || voice.state === "recording";
	// The composer docks to the bottom as the chat's does, on the same keyboard
	// avoidance: its resting inset closes to the keyboard gap as the keys rise.
	// On iOS the page sits below the stack's bar, so the avoider counts it; on
	// Android it runs under the clear pill bar, and the offset is iOS's alone.
	const chatKeyboard = useChatKeyboard({ underHeader: SYSTEM_GLASS_HEADER });
	const glassInset = useGlassBarInset();
	const keyboard = useReanimatedKeyboardAnimation();
	const { restingInset, dockRiseOverride } = chatKeyboard;
	const dockRise = useAnimatedStyle(() => ({
		transform: [{ translateY: (restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value) }],
	}));
	// A started request can't be called back, so the page holds while it runs.
	// A ref, not state, so the success path can let go and replace the page in
	// the same tick.
	const holding = useRef(false);
	useEffect(() => navigation.addListener("beforeRemove", (event) => {
		if (holding.current) event.preventDefault();
	}), [navigation]);

	// Seed from this machine's routed project, the project last worked in on it,
	// or the selected machine's active project. A project id is never carried
	// across machines. Waits for the stored last project, or a board filter
	// seeded first would stick.
	const lastProject = useLastProject(selectedHostId);
	useEffect(() => {
		// Moved this commit by the choosing effect: these values are still the
		// machine being left, and its project would replace the reset's.
		if (selectionTarget.current !== selectedHostId) return;
		if (!lastProject.loaded) return;
		const resolved = resolveSpawnProject(
			// Standalone holds only where it is offered: a machine always, Cloud when its control plane says so.
			projectId === STANDALONE_PROJECT && cloudSelected && !cloudStandaloneOffered ? null : projectId,
			routeHostId === selectedHostId ? routeProjectId : undefined,
			selectedHostId === currentHostId ? activeProjectId : ALL_PROJECTS,
			projects,
			projectsKnown,
			lastProject.projectId,
		);
		// A Cloud with no projects yet still starts a standalone agent.
		const nextProjectId = resolved ?? (cloudStandaloneOffered && projectsKnown && projects.length === 0 ? STANDALONE_PROJECT : null);
		if (nextProjectId !== projectId) changeProject(nextProjectId);
	}, [activeProjectId, cloudSelected, cloudStandaloneOffered, currentHostId, lastProject.loaded, lastProject.projectId, projects, projectsKnown, projectId, routeHostId, routeProjectId, selectedHostId]);

	// Orchestrator Cloud's catalog: every cloud agent, the connected ones usable.
	useEffect(() => {
		if (!cloudSelected) return;
		setCatalog(cloudAgentCatalog(cloudState.connections));
		setChatHarnesses([...cloudState.chatHarnesses]);
		setCatalogError(cloudState.connectionsLoaded && !cloudAgentsKey ? "Connect a coding agent first: Settings → Orchestrator Cloud." : null);
		setLoading(!cloudState.connectionsLoaded);
		// eslint-disable-next-line react-hooks/exhaustive-deps -- cloudAgentsKey covers the connections read here
	}, [cloudAgentsKey, cloudSelected, cloudState.chatHarnesses, cloudState.connectionsLoaded]);

	useEffect(() => {
		if (cloudSelected) return;
		const requestedHostId = selectedHostId;
		if (!requestedHostId) { loadedFor.current.catalog = undefined; setCatalog(null); setChatHarnesses([]); setLoading(false); return; }
		const wanted = `${catalogGeneration.current}|${epoch}|${requestedHostId}|${reloadKey}`;
		if (!config) { setLoading(false); return; }
		if (loadedFor.current.catalog === wanted) return;
		let cancelled = false;
		const generation = catalogGeneration.current;
		setLoading(true);
		Promise.all([getAgents(config), getSettings(config)])
			.then(([c, settings]) => {
				if (cancelled || !spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) return;
				loadedFor.current.catalog = wanted;
				setCatalog(c);
				setChatHarnesses(settings.chatHarnesses);
				setCatalogError(null);
			})
			.catch((e) => {
				// Previously swallowed into `catalog = null`, which left an empty
				// picker and no way to tell the daemon was unreachable.
				if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setCatalogError(agentErrorCopy(e));
			})
			.finally(() => {
				if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setLoading(false);
			});
		return () => {
			cancelled = true;
		};
		// Keyed on whether the machine is connected and on which connection it is
		// (`epoch`), not on the connection object, which an app switch replaces.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [cloudSelected, connected, epoch, reloadKey, selectedHostId]);

	// Refreshing the catalog moved into the agent sheet route, which owns its own
	// copy of it — see app/sheets/agent.tsx.
	const allAgents = useMemo(() => rankAgents(catalog), [catalog]);
	const agents = useMemo(() => mode === "chat" ? allAgents.filter((agent) => chatHarnesses.includes(agent.id)) : allAgents, [allAgents, chatHarnesses, mode]);
	const projectWorkerAgent = projectDetail?.config?.worker?.agent ?? projectDetail?.agent ?? "";
	const projectWorkerModel = projectDetail?.config?.worker?.agentConfig?.model ?? projectDetail?.config?.agentConfig?.model ?? "";
	const resolvedModel = resolveSpawnModel({ selectedAgent: harness, projectWorkerAgent, projectWorkerModel });
	// The mode the project pins for the agent (a mode-list agent such as Amp), which the daemon applies.
	const projectWorkerMode = projectDetail?.config?.worker?.agentConfig?.mode ?? projectDetail?.config?.agentConfig?.mode ?? "";
	const resolvedMode = resolveSpawnModel({ selectedAgent: harness, projectWorkerAgent, projectWorkerModel: projectWorkerMode });
	const displayedModel = modelTouched ? model : resolvedModel;
	// The project-aware catalog once it lists something. An empty one (still
	// discovering, or a project that names none) must not hide the machine's
	// list, which is what left the menu offering only the default.
	const currentModels = modelCatalog?.models.length ? modelCatalog.models : agentModelLists[harness] ?? [];
	// "Default model" when nothing is picked and the project pins nothing: the
	// default row then names the catalog's default, which the task sends
	// (modelOverride). Not "Automatic", which reads as an approval mode.
	const displayedModelLabel = displayedModel ? menuModelName(currentModels.find((item) => item.id === displayedModel)?.label ?? displayedModel) : "Default model";
	const projectApproval = projectDetail?.config?.worker?.agentConfig?.permissions ?? projectDetail?.config?.agentConfig?.permissions;
	const approvalMode = resolveApprovalMode({ harness, chosen: approvalChoice, projectMode: projectApproval });
	const modelSelection = modelTouched ? model : "__auto__";
	// The model the default row names ("Opus 5.5 · Default"), which the task
	// sends when nothing is picked (modelOverride): from this agent's own loaded
	// catalog, never one still shown from the agent picked before; none for a
	// mode list or over the project's pinned model or mode. On Cloud, while the
	// agent's catalog is missing (reloading after a project change, or its load
	// failed), the menu names the default from the agent's menu list, which is
	// the same Cloud catalog (always models, never modes), so that is sent.
	const defaultRowCatalog = modelCatalog?.agentId === harness
		? modelCatalog
		: cloudSelected && agentModelLists[harness]?.length ? { selectionMode: "catalog" as const, models: agentModelLists[harness] } : undefined;
	// The project's pin is known once its details have loaded for this project;
	// until then, or when that load failed, no default is sent and the server
	// applies whatever the project pins. A paired machine's catalog is the
	// machine's, so its default never reflects the pin.
	const projectPinKnown = !projectId || projectId === STANDALONE_PROJECT || (projectDetailLoadedFor === projectId && projectDetail?.id === projectId);
	// On a paired machine Start waits for them: sent before, an unpinned
	// project's task would carry no model and run the agent's own default.
	const projectDetailsPending = !cloudSelected && Boolean(projectId) && projectId !== STANDALONE_PROJECT && projectDetailLoadedFor !== projectId;
	const defaultRowModel = defaultRowModelToSend({ catalog: defaultRowCatalog, projectPin: resolvedModel || resolvedMode, pinKnown: projectPinKnown });
	// The model the task will run, picked or the catalog's default, for its
	// effort levels.
	const runningModel = displayedModel ? currentModels.find((item) => item.id === displayedModel) : currentModels.find((item) => item.isDefault);
	// A Claude family alias ("opus", Claude Code's default) takes its newest
	// model's levels. With nothing picked and no default named, the levels are
	// a stand-in's, which picking one pins.
	const aliasModel = harness === "claude-code" && runningModel && !runningModel.efforts?.length ? claudeAliasModel(currentModels, runningModel.id) : undefined;
	const effortModel = aliasModel ?? runningModel ?? (displayedModel ? undefined : defaultEffortModel(harness, currentModels));
	const effortChoices = useMemo(() => spawnEffortChoices(effortModel, effort), [effort, effortModel]);
	// A level picked here is sent; so is the fallback shown for a model that
	// names no default. Not a stand-in's fallback: no model is sent with it.
	const usingStandIn = !runningModel && effortModel !== undefined;
	const chosenEffort = spawnEffortToSend(effortChoices, effort, effortModel, usingStandIn);
	// Each agent's menu list, built once per change rather than per keystroke:
	// some catalogs run to thousands of models.
	const agentMenuModels = useMemo(() => Object.fromEntries(agents.filter((agent) => agent.selectable).map((agent) => [
		agent.id,
		// Every model: the wheels list an agent's whole catalog, as the chat's do.
		menuModels(agent.id === harness ? currentModels : agentModelLists[agent.id] ?? [], agent.id === harness ? modelSelection : "__auto__", Number.POSITIVE_INFINITY),
	])), [agentModelLists, agents, currentModels, harness, modelSelection]);
	const hostNotices = selectedHostId === null && hostSelectionInitialized.current
		? [routeHostId ? "That machine is not connected." : "No connected machines are available."]
		: [];
	// On Free the plan is the only thing to say: the project and agent hints
	// point at entries Free hides, so they would send the person nowhere.
	const setupBlocked = planBlocksCloudSetup(planBlock);
	const cloudNotices = [
		...(planBlock ? [planBlock.message] : []),
		...(!setupBlocked && cloudSelected && !cloudStandaloneOffered && projectsKnown && projects.length === 0 ? ["Create a cloud project first: choose New cloud project from the project menu."] : []),
	];
	const notices = [...hostNotices, ...cloudNotices, ...(setupBlocked ? [] : spawnNotices({
		// One line for a machine that is gone or refused this phone, in place of
		// the same failure from every request on this screen.
		machine,
		errorStatus: selectedHost?.errorStatus,
		mode,
		loading,
		catalogLoaded: catalog !== null,
		catalogError,
		agentCount: agents.length,
		modelError,
	}))];
	const hasComposerMessage = Boolean(
		notices.length > 0
		|| attachmentError
		|| listening
		|| voice.error
		|| error
		|| offerTUI,
	);

	useEffect(() => {
		if (!cloudSelected) return;
		const project = cloudState.projects.find((item) => item.id === projectId);
		setProjectDetail(project ? cloudProjectDetail(project) : undefined);
		setProjectDetailLoadedFor(project ? project.id : null);
	}, [cloudSelected, cloudState.projects, projectId]);

	useEffect(() => {
		if (cloudSelected) return;
		const requestedHostId = selectedHostId;
		// What is cleared is not loaded: coming back to the same project loads it again.
		if (!requestedHostId || !projectId || projectId === STANDALONE_PROJECT) { loadedFor.current.project = undefined; setProjectDetail(undefined); setProjectDetailLoadedFor(null); return; }
		const wanted = `${catalogGeneration.current}|${epoch}|${requestedHostId}|${projectId}|${reloadKey}`;
		if (!config || loadedFor.current.project === wanted) return;
		let cancelled = false;
		const generation = catalogGeneration.current;
		setProjectDetailLoadedFor(null);
		getProject(config, projectId)
			.then((nextProject) => {
				if (cancelled || !spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) return;
				loadedFor.current.project = wanted;
				setProjectDetail(nextProject);
			})
			.catch((cause) => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setModelError(userFacingError(cause)); })
			.finally(() => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setProjectDetailLoadedFor(projectId); });
		return () => { cancelled = true; };
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [cloudSelected, connected, epoch, projectId, reloadKey, selectedHostId]);

	useEffect(() => {
		if (agentTouched || loading || !catalog) return;
		if (projectId && projectId !== STANDALONE_PROJECT && projectDetailLoadedFor !== projectId) return;
		const nextHarness = resolveSpawnAgent({
			projectWorkerAgent: projectDetail?.config?.worker?.agent,
			projectAgent: projectDetail?.agent,
			availableAgents: agents.filter((agent) => agent.selectable).map((agent) => agent.id),
		});
		setHarness((current) => current === nextHarness ? current : nextHarness);
	}, [agentTouched, agents, catalog, loading, projectDetail, projectDetailLoadedFor, projectId]);

	// Each agent's machine-wide models, for the agent menu's lists. Failures
	// leave that agent's list empty; the picked agent's own load reports errors.
	const agentIds = agents.filter((agent) => agent.selectable).map((agent) => agent.id).join("|");
	// Orchestrator Cloud's model lists, one per connected agent, from its catalog.
	const cloudOrgId = cloudState.org?.id;
	// While a list shown is still Cloud's release list, it is read again until
	// the account's own arrives, as the desktop does (useReleaseCatalogRereads).
	const [cloudMenuListsRelease, setCloudMenuListsRelease] = useState(false);
	const [cloudPickedListRelease, setCloudPickedListRelease] = useState(false);
	const cloudRereads = useReleaseCatalogRereads(cloudSelected && (cloudMenuListsRelease || cloudPickedListRelease));
	const cloudListsFor = useRef<string | undefined>(undefined);
	// Whether each agent's menu list is still the release list.
	const cloudListsRelease = useRef<Record<string, boolean>>({});
	// The organization the menu lists were loaded for.
	const cloudListsOrg = useRef<string | undefined>(undefined);
	useEffect(() => {
		if (!cloudSelected) { cloudListsFor.current = undefined; return; }
		if (!cloudOrgId || !agentIds) { cloudListsFor.current = undefined; cloudListsOrg.current = undefined; cloudListsRelease.current = {}; setAgentModelLists({}); setCloudMenuListsRelease(false); return; }
		// Another organization's lists go at once, not when the new ones land:
		// the default row falls back to them (defaultRowCatalog), and must never
		// send a model from an organization this task is not in.
		if (cloudListsOrg.current !== cloudOrgId) {
			cloudListsOrg.current = cloudOrgId;
			cloudListsRelease.current = {};
			setAgentModelLists({});
		}
		const key = `${cloudOrgId}/${agentIds}`;
		const reread = cloudListsFor.current === key;
		let cancelled = false;
		void Promise.all(agentIds.split("|").map(async (id) => {
			try {
				const catalog = await agentModelCatalog(cloudState.client, cloudOrgId, id, { rereadRelease: reread });
				return [id, cloudModelCatalog(id, catalog).models, isReleaseCatalog(catalog)] as const;
			} catch {
				return [id, undefined, undefined] as const;
			}
		})).then((entries) => {
			if (cancelled) return;
			cloudListsFor.current = key;
			// A failed re-read keeps the list already shown, and whether it is
			// still the release list, so its re-reads go on.
			const released = Object.fromEntries(entries.map(([id, , release]) => [id, release ?? (reread ? cloudListsRelease.current[id] : undefined) ?? false]));
			cloudListsRelease.current = released;
			setAgentModelLists((current) => Object.fromEntries(entries.map(([id, models]) => [id, models ?? (reread ? current[id] : undefined) ?? []])));
			setCloudMenuListsRelease(Object.values(released).some(Boolean));
		});
		return () => { cancelled = true; };
	}, [agentIds, cloudOrgId, cloudRereads, cloudSelected, cloudState.client]);

	useEffect(() => {
		if (cloudSelected) return;
		const requestedHostId = selectedHostId;
		if (!requestedHostId || !agentIds) { setAgentModelLists({}); return; }
		const ids = agentIds.split("|");
		const cache = agentModelCache.current;
		const scope = `${catalogGeneration.current}|${epoch}|${requestedHostId}`;
		const missing = cache.missing(scope, ids);
		// Lists on screen stay while the ones still missing load. Those are asked
		// again on a reconnect or a reload: only the agents whose list failed.
		if (!config || missing.length === 0) { setAgentModelLists(cache.lists(ids)); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		const polling = new AbortController();
		void Promise.all(missing.map(async (id) => {
			try {
				return [id, (await getAgentModels(config, id, undefined, polling.signal)).models ?? []] as const;
			} catch {
				return [id, undefined] as const;
			}
		})).then((entries) => {
			if (cancelled || !spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) return;
			for (const [id, models] of entries) if (models) cache.record(scope, id, models);
			setAgentModelLists(cache.lists(ids));
		});
		return () => { cancelled = true; polling.abort(); };
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [agentIds, cloudSelected, connected, epoch, reloadKey, selectedHostId]);

	// A new cloud task's models come from Orchestrator Cloud's catalog for the agent.
	const cloudCatalogFor = useRef<string | undefined>(undefined);
	useEffect(() => {
		if (!cloudSelected) { cloudCatalogFor.current = undefined; return; }
		if (!cloudOrgId || !harness) { cloudCatalogFor.current = undefined; setModelCatalog(undefined); setModelLoading(false); setCloudPickedListRelease(false); return; }
		// A re-read of the list shown neither clears it nor shows it loading.
		// Another project is a new load: changing it clears the catalog
		// (changeProject), and Start waits until it is back, so the default
		// row's model is there to send.
		const key = `${cloudOrgId}/${harness}/${projectId ?? ""}`;
		const reread = cloudCatalogFor.current === key;
		let cancelled = false;
		if (!reread) setModelLoading(true);
		agentModelCatalog(cloudState.client, cloudOrgId, harness, { rereadRelease: reread })
			.then((catalog) => {
				if (cancelled) return;
				cloudCatalogFor.current = key;
				setModelCatalog(cloudModelCatalog(harness, catalog));
				setCloudPickedListRelease(isReleaseCatalog(catalog));
				setModelError(undefined);
			})
			.catch(() => { if (!cancelled && !reread) setModelCatalog(undefined); })
			// Any answer ends a load: a re-read shows none, but the load it
			// replaced (another agent's, cut short) may have.
			.finally(() => { if (!cancelled) setModelLoading(false); });
		return () => { cancelled = true; };
	}, [cloudOrgId, cloudRereads, cloudSelected, cloudState.client, harness, projectId]);

	useEffect(() => {
		if (cloudSelected) return;
		const requestedHostId = selectedHostId;
		// The machine's own catalog loads before a project is chosen, so the
		// model menu is never just the default; choosing a project swaps in its.
		if (!requestedHostId || !harness) { loadedFor.current.models = undefined; setModelCatalog(undefined); setModelLoading(false); return; }
		const wanted = `${catalogGeneration.current}|${epoch}|${requestedHostId}|${harness}|${projectId ?? ""}|${reloadKey}`;
		if (!config) { setModelLoading(false); return; }
		if (loadedFor.current.models === wanted) return;
		let cancelled = false;
		const generation = catalogGeneration.current;
		setModelLoading(true);
		const polling = new AbortController();
		getAgentModels(config, harness, !projectId || projectId === STANDALONE_PROJECT ? undefined : projectId, polling.signal)
			.then((nextCatalog) => {
				if (cancelled || !spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) return;
				loadedFor.current.models = wanted;
				setModelCatalog(nextCatalog);
				setModelError(nextCatalog.warning);
			})
			.catch((cause) => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setModelError(userFacingError(cause)); })
			.finally(() => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setModelLoading(false); });
		return () => { cancelled = true; polling.abort(); };
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [cloudSelected, connected, epoch, harness, projectId, reloadKey, selectedHostId]);

	// The "/" list's skills, read from the machine when it opens, as a new chat
	// on the desktop lists them before the agent starts. A Cloud task's agent
	// runs in its sandbox, away from this machine's skill folders.
	// The open "/" list, drawn over the page above the composer (see below).
	const [suggestionOverlay, setSuggestionOverlay] = useState<SpawnSuggestionOverlay | null>(null);
	const latestConfig = useRef(config);
	latestConfig.current = config;
	const skillsProjectId = !projectId || projectId === STANDALONE_PROJECT ? undefined : projectId;
	const loadSkills = useMemo(() => {
		if (cloudSelected || !selectedHostId || !harness) return undefined;
		return () => loadSpawnSkills(latestConfig.current, selectedHostId, harness, skillsProjectId);
	}, [cloudSelected, harness, selectedHostId, skillsProjectId]);

	// Loads that failed while the desktop was unreachable run again once the
	// board's poll reconnects. Keyed on the reconnect, not on the errors, so an
	// endpoint that keeps failing while connected can't loop.
	const previousConnection = useRef(connection);
	useEffect(() => {
		const reconnected = previousConnection.current !== "open" && connection === "open";
		previousConnection.current = connection;
		if (reconnected && (catalogError || modelError)) setReloadKey((key) => key + 1);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [connection]);

	// A change the user makes to the form. The notice a dropped Start left
	// asked for one, so it goes; changes the page makes itself (an account
	// change emptying the composer) leave it.
	const edited = <A extends unknown[]>(change: (...args: A) => void) => (...args: A) => {
		setError(errorAfterEdit);
		formEdited.current = true;
		change(...args);
	};

	// A level belongs to the model it was picked for, so a new model starts at its own default.
	const clearModelOverride = () => { setModel(""); setModelTouched(false); setEffort(undefined); };
	// A cleared catalog is not loaded any more: picking the same agent or
	// project again (A, B, then A) loads it again rather than leaving it empty.
	const resetModelSource = () => { clearModelOverride(); loadedFor.current.models = undefined; setModelCatalog(undefined); setModelError(undefined); };
	// Clears what was chosen for the machine being left: a project id is never
	// carried across machines. Shared by a pick by hand and the page's own choice.
	const resetHostChoice = () => {
		catalogGeneration.current += 1;
		setProjectId(null);
		setHarness("");
		setAgentTouched(false);
		loadedFor.current.catalog = undefined;
		loadedFor.current.project = undefined;
		setCatalog(null);
		setCatalogError(null);
		setChatHarnesses([]);
		setProjectDetail(undefined);
		setProjectDetailLoadedFor(null);
		setModelLoading(false);
		resetModelSource();
		setAgentModelLists({});
		setApprovalChoice(undefined);
		setError(null);
		setOfferTUI(false);
		requestRef.current = undefined;
	};
	const chooseHost = (nextHostId: string) => {
		if (nextHostId === selectedHostId) return;
		// Picked by hand: the page stops choosing for the person.
		hostSettled.current = true;
		resetHostChoice();
		// Cloud is never the default machine: choosing it here leaves that alone.
		void selectSpawnHost(nextHostId, { select: selectHost, persistDefault: isCloudHost(nextHostId) ? async () => {} : switchHost })
			.catch(() => setError("Could not make that machine the default. Try again."));
	};
	const changeProject = (nextProjectId: string | null) => {
		if (nextProjectId === NEW_CLOUD_PROJECT) {
			// A cloud project starts from a repository, on its own sheet over this
			// page; the project menu offers it once it exists.
			router.push({ pathname: "/cloud/new-project", params: { placement: "cloud" } });
			return;
		}
		if (!spawnModelSourceChanged({ projectId, agentId: harness }, { projectId: nextProjectId, agentId: harness })) return;
		resetModelSource();
		loadedFor.current.project = undefined;
		setProjectDetail(undefined);
		setProjectDetailLoadedFor(null);
		setAgentTouched(false);
		setProjectId(nextProjectId);
	};
	const selectAgent = (nextHarness: string) => {
		if (!spawnModelSourceChanged({ projectId, agentId: harness }, { projectId, agentId: nextHarness })) return;
		resetModelSource();
		setAgentTouched(true);
		setHarness(nextHarness);
	};
	const selectMode = (nextMode: SessionMode) => {
		if (nextMode === mode) return;
		const nextHarness = nextMode === "chat"
			? (chatHarnesses.includes(harness) ? harness : (defaultAgent(allAgents.filter((agent) => chatHarnesses.includes(agent.id))) ?? ""))
			: (harness || (defaultAgent(allAgents) ?? ""));
		if (spawnModelSourceChanged({ projectId, agentId: harness }, { projectId, agentId: nextHarness })) {
			resetModelSource();
			setAgentTouched(false);
		}
		setMode(nextMode);
		setHarness(nextHarness);
	};
	const selectModel = (nextModel: string) => {
		if (nextModel === "__auto__") {
			clearModelOverride();
			return;
		}
		if (nextModel !== model) setEffort(undefined);
		setModel(nextModel);
		setModelTouched(true);
	};
	const selectEffort = (nextEffort: string) => {
		if (!runningModel && effortModel) {
			setModel(effortModel.id);
			setModelTouched(true);
		}
		setEffort(nextEffort);
	};
	// The agent menu picks an agent and one of its models together.
	const selectAgentModel = (nextHarness: string, nextModel: string) => {
		selectAgent(nextHarness);
		selectModel(nextModel);
	};
	// An agent's whole catalog, searchable, for one too long for its menu.
	const browseModels = (agentId: string) => {
		router.push(modelSheetRoute({
			agentId,
			projectId: projectId && projectId !== STANDALONE_PROJECT ? projectId : "",
			selected: agentId === harness && modelTouched ? model : "",
			// A cloud task browses Orchestrator Cloud's catalog, not the machine's.
			...(cloudSelected ? { hostId: CLOUD_HOST_ID } : {}),
			// The sheet's default row answers "".
			onSelect: edited((id: string) => selectAgentModel(agentId, id || "__auto__")),
		}));
	};
	/** Adds picks to the task; resolves to how many were added. `notice` says what a paste left out. */
	const addAttachments = async (pick: () => Promise<PickedSpawnAttachment[] | null>, fallback: string, notice?: string): Promise<number> => {
		if (pickingAttachments.current) return 0;
		if (cloudSelected) {
			setAttachmentError("Cloud tasks can't take attachments yet. Start the task, then add files in its chat.");
			return 0;
		}
		pickingAttachments.current = true;
		setAttachmentError(undefined);
		// The pick's own result would replace the paste-again notice, so it is
		// added back to whatever the pick ends with.
		const missed = () => {
			const was = missedPaste.current;
			missedPaste.current = false;
			return was ? "Paste the images again: they came in while attachments were loading." : undefined;
		};
		// The account the pick was made under: another signed in while it loaded
		// emptied the composer, and the files are not the next account's.
		const pickedFor = draftAccountRef.current;
		try {
			const picked = await pick();
			if (draftAccountRef.current !== pickedFor) return 0;
			if (!picked) { setAttachmentError(missed()); return 0; }
			const before = attachmentsRef.current;
			const next = await readSpawnAttachments(before, picked);
			if (draftAccountRef.current !== pickedFor) return 0;
			const merged = appendSpawnAttachments(attachmentsRef.current, next.attachments.slice(before.length));
			const added = merged.attachments.length - attachmentsRef.current.length;
			attachmentsRef.current = merged.attachments;
			setAttachments(merged.attachments);
			setAttachmentError(joinAttachmentErrors(next.error ?? merged.error, notice, missed()));
			return added;
		} catch (cause) {
			setAttachmentError(joinAttachmentErrors(userFacingError(cause, fallback), missed()));
			return 0;
		} finally {
			pickingAttachments.current = false;
		}
	};
	const pickFiles = () => addAttachments(async () => {
		const result = await DocumentPicker.getDocumentAsync({
			multiple: true,
			copyToCacheDirectory: true,
			type: "*/*",
		});
		if (result.canceled) return null;
		return result.assets.map((asset) => {
			const file = new File(asset.uri);
			return {
				name: asset.name,
				mimeType: asset.mimeType || "application/octet-stream",
				bytes: asset.size ?? file.size,
				previewUri: asset.uri,
				readData: () => file.base64(),
			};
		});
	}, "Couldn't read that file.");
	const pickPhotos = () => addAttachments(async () => {
		const remaining = MAX_SPAWN_ATTACHMENTS - attachmentsRef.current.length;
		if (remaining <= 0) throw new Error(`You can attach up to ${MAX_SPAWN_ATTACHMENTS} files.`);
		// "compatible" asks iOS for a JPEG rather than the library's HEIC
		// original, which most agents can't open.
		const result = await ImagePicker.launchImageLibraryAsync({
			mediaTypes: ["images"],
			quality: 0.82,
			allowsMultipleSelection: true,
			selectionLimit: remaining,
			preferredAssetRepresentationMode: ImagePicker.UIImagePickerPreferredAssetRepresentationMode.Compatible,
		});
		if (result.canceled) return null;
		return result.assets.map((asset, index) => photoPickerAttachment(asset, index, new File(asset.uri)));
	}, "Couldn't open your photo library.");

	// Pasting is a way to attach too, so it goes through the same gate as the
	// menu. Native stops a paste at 8 images or 25 MB and says which.
	const pasteImages = (assets: ComposerImageAsset[], overflow?: PasteOverflow) => {
		// A paste can't wait for a pick still loading, and dropping it silently
		// would look like paste doing nothing.
		if (pickingAttachments.current) {
			missedPaste.current = true;
			setAttachmentError("Wait for attachments to finish loading, then paste again.");
			return;
		}
		const { picks, error } = pastedSpawnAttachments(assets, attachmentsRef.current.length);
		const skipped = overflow === "count" ? `You can attach up to ${MAX_SPAWN_ATTACHMENTS} files.` : overflow === "size" ? "Pasted images must total under 25 MB." : undefined;
		void addAttachments(async () => picks, "Couldn't read the pasted image.", joinAttachmentErrors(error, skipped))
			.then((added) => { if (added > 0) haptics.tap(); });
	};

	const onSpawn = async () => {
		if (!selectedHostId || (!config && !cloudSelected)) {
			setError("Choose a connected machine before starting this task.");
			return;
		}
		if (pickingAttachments.current) {
			setAttachmentError("Wait for attachments to finish loading.");
			return;
		}
		// Validated on submit rather than by disabling the button — desktop's
		// choice, and the better one: a disabled button with no explanation is
		// worse than a message naming what is missing.
		setBusy(true);
		holding.current = true;
		setError(null);
		setOfferTUI(false);
		try {
			const request = {
				hostId: selectedHostId,
				projectId: projectId ?? undefined,
				prompt: prompt.trim() || undefined,
				harness: harness || undefined,
				model: modelOverride(displayedModel, modelTouched, defaultRowModel),
				// Only a level picked here; otherwise the model's default applies.
				effort: chosenEffort,
				accountId: accountPick || undefined,
				// Only a mode picked here overrides the project's; an untouched
				// picker names the project's mode, which the daemon applies anyway.
				approvalMode: approvalChoice === approvalMode ? approvalChoice : undefined,
				mode,
				attachments: attachmentsRef.current.map(({ mimeType, data }) => ({ mimeType, data })),
			};
			const { attachments: _, ...requestFields } = request;
			// A request is what the person chose. The default row's model is the
			// catalog's, which a re-read can change between a Start whose answer
			// was lost and its retry: the retry keeps the id, and the model it
			// went with, so the server matches it instead of starting a second worker.
			const payload = JSON.stringify({ ...requestFields, model: modelOverride(displayedModel, modelTouched) });
			// Compared by content, not by object: attachments read back from a
			// restored draft are the same files the last Start sent.
			const sent: SpawnDraftRequest = requestMatches(requestRef.current, payload, attachmentsRef.current)
				? requestRef.current
				: { payload, attachments: attachmentsKey(attachmentsRef.current), id: `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`, ...(request.model ? { model: request.model } : {}) };
			requestRef.current = sent;
			// The request id is on the phone before the request leaves: closed
			// after the daemon took the task but before the draft is cleared, the
			// restored draft's Start reuses it, and the daemon answers with that
			// task. A save that fails does not hold the task back; the draft is
			// deleted instead, so none without this id can be restored and sent again.
			const saved = await saveBeforeStart(draftSaver, { prompt, attachments: attachmentsRef.current, owner: draftOwner, selections: draftSelections, request: sent });
			// Dropped while it waited: the account changed or this machine was
			// forgotten, which emptied the draft, and the task it held does not
			// start. It says so, rather than Start seeming to do nothing.
			if (saved === "dropped") {
				holding.current = false;
				haptics.error();
				setError(START_DROPPED_NOTICE);
				setBusy(false);
				return;
			}
			const session = await spawn({ ...request, model: sent.model, clientRequestId: sent.id });
			// Started: the draft goes, so the next New task opens empty. Only this
			// Start's: another open page's draft saved meanwhile stays.
			startedDraft.current = { prompt, attachments: attachmentsRef.current };
			void draftSaver.cancel().then(() => clearStartedSpawnDraft(sent.id)).catch(() => {});
			if (projectId === STANDALONE_PROJECT) setActiveProject(ALL_PROJECTS);
			else rememberLastProject(selectedHostId, projectId);
			holding.current = false;
			// Covered while the request ran (a notification or link can still
			// open a page over this one): the worker shows up on the board, and
			// replacing this page now would swap out one the user can't see. The
			// page lets go again, or coming back to it would find it locked for
			// good; a second Start reuses the request id, so it can't spawn a
			// second worker.
			if (!navigation.isFocused()) {
				setBusy(false);
				return;
			}
			haptics.success();
			// The new task becomes its conversation in place, as a new chat does:
			// the session replaces this page, so going back returns to wherever
			// the task was started from. The session screen shows its own
			// "connecting" state while the agent attaches.
			router.replace({
				pathname: "/session/[id]",
				params: { id: session.id, projectId: session.projectId, hostId: selectedHostId },
			});
		} catch (e) {
			holding.current = false;
			haptics.error();
			// Refused by the server, no task started: the next Start goes under a
			// new id, with the model shown then rather than this one's default.
			if (startRefused(e)) requestRef.current = undefined;
			setError(spawnErrorCopy(e));
			setOfferTUI(mode === "chat" && isChatPreflightError(e));
			setBusy(false);
		}
	};

	// While a request runs, the back swipe is off too, so it never starts a
	// transition the hold above would cancel.
	useEffect(() => {
		if (!busy) return;
		navigation.setOptions({ gestureEnabled: false });
		return () => navigation.setOptions({ gestureEnabled: true });
	}, [busy, navigation]);

	const harnessLabel = agents.find((item) => item.id === harness)?.label;
	// Before the list lands the seed can name a project the picker doesn't list
	// yet. It shows under its id, the name it was created with, so Start never
	// sends a task somewhere the picker calls "Choose project".
	const unlistedProject = projectId && projectId !== STANDALONE_PROJECT && projectId !== NEW_CLOUD_PROJECT && !projects.some((item) => item.id === projectId)
		? [{ id: projectId, label: projectId, icon: "folder" as const }]
		: [];

	return (
		<KeyboardAvoidingView
			style={[styles.screen, { paddingTop: glassInset }, chatKeyboard.rootStyle]}
			behavior={chatKeyboard.behavior}
			keyboardVerticalOffset={chatKeyboard.keyboardVerticalOffset}
		>
			{/* The room and the dock share one unpadded frame, so the "/" list's
			    layer below can be measured from the dock's own bottom. */}
			<View style={styles.stage}>
			{/* The empty conversation the task will become. A tap there puts the
			    keyboard away, as it does over a conversation. */}
			<Pressable accessible={false} style={styles.room} onPress={Keyboard.dismiss} />

			<SpawnSuggestionHost.Provider value={setSuggestionOverlay}>
			<Animated.View style={[styles.dock, { paddingBottom: restingInset }, dockRise]}>
				{hasComposerMessage ? <View style={styles.messages}>
					{notices.map((notice) => <Text key={notice} style={styles.warn}>{notice}</Text>)}
					{attachmentError ? <Text style={styles.warn}>{attachmentError}</Text> : null}
					{voice.error ? <Text accessibilityRole="alert" style={styles.warn}>{voice.error}</Text> : null}
					{error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
					{offerTUI ? <Button title="Create as Terminal UI instead" variant="secondary" icon="terminal" onPress={() => { selectMode("tui"); setOfferTUI(false); setError(null); }} /> : null}
					{planBlock ? <Button title="See plan" variant="secondary" icon="info" onPress={() => router.push("/sheets/cloud-plan")} /> : null}
				</View> : null}

				<SpawnComposerControls
					hosts={hostOptions}
					hostId={selectedHostId}
					onSelectHost={edited(chooseHost)}
					projects={cloudSelected
						// A cloud project is created from here; a standalone agent, where
						// offered, needs none.
						? cloudSpawnProjectOptions(
							[...unlistedProject, ...projects.map((item) => ({ id: item.id, label: item.name, icon: "folder" as const }))],
							cloudStandaloneOffered,
						)
						: [...unlistedProject, ...projects.map((item) => ({ id: item.id, label: item.name, icon: "folder" as const })), { id: STANDALONE_PROJECT, label: "Standalone agent", icon: "message-square-plus" as const, sectionBreakBefore: true }]}
					projectId={projectId}
					onSelectProject={edited(changeProject)}
					accounts={accountOptions}
					accountId={shownAccountId}
					onSelectAccount={edited(setAccountPick)}
					agents={agents.filter((item) => item.selectable).map((item) => ({ id: item.id, label: item.label }))}
					harness={harness}
					onSelectHarness={edited(selectAgent)}
					models={currentModels.map((item) => ({ id: item.id, label: item.label }))}
					agentModels={agentMenuModels}
					modelSelection={modelSelection}
					modelLabel={displayedModelLabel}
					onSelectModel={edited(selectModel)}
					onSelectAgentModel={edited(selectAgentModel)}
					usesDefaultModel={!displayedModel}
					onBrowseModels={browseModels}
					approvalModes={approvalOptions(harness)}
					approvalMode={approvalMode}
					onSelectApprovalMode={edited((next: string) => setApprovalChoice(next as ApprovalMode))}
					effortChoices={effortChoices}
					onSelectEffort={edited(selectEffort)}
					onChoosePhoto={edited(() => { void pickPhotos(); })}
					onChooseFile={edited(() => { void pickFiles(); })}
					onPasteImages={busy ? null : edited(pasteImages)}
					voice={{ state: voice.state, partial: voice.partial, subscribeLevel: voice.subscribeLevel, onToggleLatched: voice.toggleLatched }}
					prompt={prompt}
					onChangePrompt={edited(setPrompt)}
					onLoadSkills={loadSkills}
					placeholder={harnessLabel ? `Ask ${harnessLabel}` : "What should this worker do?"}
					attachments={<ComposerAttachmentTray
						items={trayItems}
						onRemove={edited((key: string) => {
							const item = attachments[trayItems.findIndex((candidate) => candidate.key === key)];
							if (!item) return;
							attachmentsRef.current = attachmentsRef.current.filter((candidate) => candidate !== item);
							setAttachments(attachmentsRef.current);
						})}
					/>}
					onSpawn={() => { void onSpawn(); }}
					busy={busy}
					disabled={!selectedHostId || (!config && !cloudSelected) || !projectId || !harness || busy || modelLoading || projectDetailsPending || loading || listening || voice.state === "transcribing" || Boolean(planBlock)}
				/>
			</Animated.View>
			</SpawnSuggestionHost.Provider>

			{/* The "/" list floats over the page, just above the composer, rather
			    than taking room in the dock: the machine and project rows stay put
			    under it. It rises with the dock over the keyboard, and only the list
			    itself takes touches. It stands on the page's own colour, as the
			    chat's list stands on its dock's fade, so the rows under it don't
			    show through its glass. */}
			{suggestionOverlay ? <Animated.View
				pointerEvents="box-none"
				style={[styles.suggestionLayer, { paddingBottom: restingInset + suggestionOverlay.composerHeight + space.xs }, dockRise]}
			>
				<View style={styles.suggestionBackdrop}>{suggestionOverlay.panel}</View>
			</Animated.View> : null}
			</View>
		</KeyboardAvoidingView>
	);
}

// Human copy for a failed spawn, matching every other screen. Never the wire
// string ("401 Unauthorized - missing or invalid connection password").
function spawnErrorCopy(e: unknown): string {
	if (isChatPreflightError(e)) return chatErrorCopy(e);
	if (e instanceof ApiError && e.code === "PROMPT_TOO_LONG") {
		return "Task prompt is too long. Keep it to 100 KiB or fewer (emoji and other non-English characters use more than one byte). Shorten it and try again.";
	}
	return userFacingError(e, "Couldn't start the worker. Try again.");
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// The page a conversation is drawn on, since this is where one begins.
		screen: { flex: 1, backgroundColor: t.bgBase },
		stage: { flex: 1 },
		room: { flex: 1 },
		// Spans the stage, the list at its foot: in the dock's side margin, and
		// over the composer by the composer's height and the dock's gap.
		suggestionLayer: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, paddingHorizontal: space.sm, justifyContent: "flex-end" },
		// The list's own corners (ComposerSuggestionPanel), filled with the page.
		suggestionBackdrop: { backgroundColor: t.bgBase, borderRadius: radius.xl, borderCurve: "continuous" },
		// The chat composer's dock: its side margin and the gap to the keyboard.
		dock: { paddingHorizontal: space.sm, paddingTop: space.xs, gap: space.xs },
		messages: { gap: space.xs, paddingHorizontal: space.xs },
		warn: { fontFamily: "DMSans_400Regular", color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		error: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
