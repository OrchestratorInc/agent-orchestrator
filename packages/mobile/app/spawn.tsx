import { useLocalSearchParams, useRouter } from "expo-router";
import { Feather } from "../lib/icons";
import BottomSheet, { BottomSheetView } from "@expo/ui/community/bottom-sheet";
import * as DocumentPicker from "expo-document-picker";
import { File } from "expo-file-system";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	InteractionManager,
	Alert,
	Platform,
	Pressable,
	ScrollView,
	StyleSheet,
	Text,
	TextInput,
	View,
} from "react-native";
import { KeyboardStickyView, useKeyboardState } from "react-native-keyboard-controller";
import { agentErrorCopy } from "../lib/agentError";
import { defaultAgent, rankAgents } from "../lib/agentPicker";
import { ApiError, getAgentModels, getAgents, getProject, getSettings, type AgentCatalog, type AgentInfo, type AgentModelCatalog, type ProjectDetail, type SessionMode } from "../lib/api";
import { userFacingError } from "../lib/connectionError";
import { chatErrorCopy, isChatPreflightError } from "../lib/chatError";
import { readyHarnesses } from "../lib/cloud/agentReadiness";
import { useCloudAuth } from "../lib/cloud/authStore";
import { cloudRequestErrorCopy } from "../lib/cloud/errorCopy";
import { sourceKey, type SourceRef } from "../lib/environment/scopedBoard";
import { haptics } from "../lib/haptics";
import { ALL_PROJECTS, STANDALONE_PROJECT, resolveSpawnProject } from "../lib/projectFilter";
import { cloudRoleDefaults, modelOverride, resolveSpawnAgent, resolveSpawnModel, spawnModelSourceChanged, spawnRequestFingerprint } from "../lib/spawnModel";
import { clearCloudOperation, readCloudOperation, type CloudOperation } from "../lib/cloud/operation";
import type { CreateSessionInput } from "@aoagents/cloud-client";
import { appendSpawnAttachments, readSpawnAttachments, type SpawnAttachment } from "../lib/spawn-attachments";
import { SpawnComposerControls } from "../lib/spawn-composer-controls";
import { canSubmitSpawn, initialSpawnDestination, spawnRequestIsCurrent } from "../lib/spawnDestination";
import { loadSpawnPreference, saveSpawnPreference, type SpawnPreference } from "../lib/spawnPreference";
import { spawnNotices } from "../lib/spawnNotices";
import { SpawnPromptInput } from "../lib/spawn-prompt-input";
import { useApp } from "../lib/store";
import { useVoiceInput } from "../lib/voice/useVoiceInput";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { textSelectionColors } from "../lib/textSelection";
import { Button } from "../lib/ui";
import { iconSize, space, type } from "../lib/tokens";
import { backOr } from "../lib/backNavigation";
import { routeSource } from "../lib/session/sessionRoute";

export { SheetErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

export default function SpawnModal() {
	return <SpawnModalContent />;
}

function SpawnModalContent() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const { projectId: routeProjectId, source: routeKind, sourceId: routeSourceId, hostId: routeHostId } = useLocalSearchParams<{ projectId?: string; source?: string; sourceId?: string; hostId?: string }>();
	const { scopedBoard, availableSources, sourceFor, spawnOn, configForHost, hostStates, configResolved, switchHost, setActiveProject } = useApp();
	const cloudAuth = useCloudAuth();
	const { client: cloudClient, orgId: cloudOrgId } = cloudAuth;
	const cloudScopeKey = cloudOrgId ? sourceFor({ kind: "cloud", id: cloudOrgId })?.scopeKey : undefined;
	const mounted = useRef(true);
	const submitting = useRef(false);
	useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
	const routeDestination = routeSource({ source: routeKind, sourceId: routeSourceId }) ?? (routeHostId ? { kind: "local" as const, id: routeHostId } : null);
	const sourceResolutionComplete = configResolved;
	const [destination, setDestination] = useState<SourceRef | null>(null);
	const destinationRef = useRef<SourceRef | null>(null);
	const [savedPreference, setSavedPreference] = useState<SpawnPreference | null>(null);
	const [preferenceLoaded, setPreferenceLoaded] = useState(false);
	const catalogGeneration = useRef(0);
	const cloudSpawn = destination?.kind === "cloud";
	const config = destination?.kind === "local" ? configForHost(destination.id) : null;
	const selectedHost = destination?.kind === "local" ? hostStates.find((host) => host.hostId === destination.id) : undefined;
	const connection = selectedHost?.connection ?? "closed";
	const unreachable = connection === "closed" && !!selectedHost?.error;
	const projects = useMemo(() => destination
		? scopedBoard.projects.filter((entry) => sourceKey(entry.source) === sourceKey(destination)).map((entry) => entry.value)
		: [], [destination, scopedBoard.projects]);
	const projectsKnown = destination !== null && !!scopedBoard.sources[sourceKey(destination)]?.resolved && !scopedBoard.sources[sourceKey(destination)]?.loading;
	const destinations = availableSources.map((source) => ({ source, label: source.kind === "local" ? hostStates.find((host) => host.hostId === source.id)?.name ?? "Desktop" : "Cloud", available: !!sourceFor(source), unavailableReason: scopedBoard.sources[sourceKey(source)]?.error ?? null }));

	const [projectId, setProjectId] = useState<string | null>(null);
	const [harness, setHarness] = useState("");
	const [agentTouched, setAgentTouched] = useState(false);
	const [mode, setMode] = useState<SessionMode>("chat");
	const [chatHarnesses, setChatHarnesses] = useState<string[]>([]);
	const [prompt, setPrompt] = useState("");
	const [attachments, setAttachments] = useState<SpawnAttachment[]>([]);
	const attachmentsRef = useRef<SpawnAttachment[]>([]);
	const pickingAttachments = useRef(false);
	const [attachmentError, setAttachmentError] = useState<string>();
	const [model, setModel] = useState("");
	const [modelTouched, setModelTouched] = useState(false);
	const [customModelOpen, setCustomModelOpen] = useState(false);
	const [customModel, setCustomModel] = useState("");
	const [reservedWorker, setReservedWorker] = useState<CloudOperation<CreateSessionInput> | null>(null);
	const [operationLoading, setOperationLoading] = useState(false);
	const [modelCatalog, setModelCatalog] = useState<AgentModelCatalog>();
	const [projectDetail, setProjectDetail] = useState<ProjectDetail>();
	const [projectDetailLoadedFor, setProjectDetailLoadedFor] = useState<string | null>(null);
	const [modelLoading, setModelLoading] = useState(false);
	const [modelError, setModelError] = useState<string>();
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const requestRef = useRef<{ payload: string; attachments: readonly SpawnAttachment[]; id: string } | undefined>(undefined);

	const [catalog, setCatalog] = useState<AgentCatalog | null>(null);
	const [catalogError, setCatalogError] = useState<string | null>(null);
	const [loading, setLoading] = useState(true);
	const [offerTUI, setOfferTUI] = useState(false);
	// Bumped to re-run the loads below after the desktop comes back.
	const [reloadKey, setReloadKey] = useState(0);
	// Spoken text lands in the prompt the way it does in the chat composer:
	// appended, so dictation can extend what was typed rather than replace it.
	const voice = useVoiceInput({ onTranscript: useCallback((spoken: string) => setPrompt((old) => old ? `${old} ${spoken}` : spoken), []) });
	const listening = voice.state === "starting" || voice.state === "recording";
	// iOS: the prompt fills the room above the controls. The controls translate
	// with the keyboard, which does not reflow their siblings; a spacer below
	// the attachments and messages makes that entire region reflow instead.
	const [promptRoom, setPromptRoom] = useState<number>();
	const keyboardHeight = useKeyboardState((state) => state.height);
	const previousCloudScope = useRef(cloudScopeKey);
	useEffect(() => {
		if (previousCloudScope.current === cloudScopeKey) return;
		previousCloudScope.current = cloudScopeKey;
		catalogGeneration.current += 1;
		setReservedWorker(null); requestRef.current = undefined;
		if (destinationRef.current?.kind === "cloud") {
			destinationRef.current = null; setDestination(null); setProjectId(null);
			setHarness(""); setAgentTouched(false); setModel(""); setModelTouched(false); setPrompt("");
		}
	}, [cloudScopeKey]);
	useEffect(() => {
		let cancelled = false;
		setReservedWorker(null);
		if (!cloudSpawn || !cloudScopeKey || !projectId) { setOperationLoading(false); return; }
		setOperationLoading(true);
		void readCloudOperation<CreateSessionInput>(cloudScopeKey, `worker-create/${projectId}`).then((operation) => {
			if (cancelled || !cloudAuth.isCurrentSession() || !operation) return;
			setReservedWorker(operation);
			const payload = operation.payload;
			setPrompt(payload.prompt ?? ""); setHarness(payload.harness); setAgentTouched(true);
			setModel(payload.model ?? ""); setModelTouched(true);
			requestRef.current = { payload: spawnRequestFingerprint(cloudScopeKey, `cloud/${destination!.id}`, { ...payload, mode: "chat" }), attachments: attachmentsRef.current, id: operation.key };
		}).catch((cause) => { if (!cancelled) setError(userFacingError(cause)); })
			.finally(() => { if (!cancelled) setOperationLoading(false); });
		return () => { cancelled = true; };
	}, [cloudSpawn, cloudScopeKey, projectId]);



	useEffect(() => {
		let cancelled = false;
		void loadSpawnPreference(cloudScopeKey).then((preference) => {
			if (cancelled) return;
			setSavedPreference(preference);
			setPreferenceLoaded(true);
		});
		return () => { cancelled = true; };
	}, [cloudScopeKey]);

	useEffect(() => {
		if (!sourceResolutionComplete || !preferenceLoaded) return;
		const routed = routeDestination && routeDestination.kind !== "invalid" ? routeDestination : null;
		if (routeDestination?.kind === "invalid") return;
		const validPreference = savedPreference?.source.kind !== "cloud" || savedPreference.scopeKey === cloudScopeKey ? savedPreference : null;
		const next = initialSpawnDestination(routed, availableSources, validPreference?.source);
		if (destinationRef.current && availableSources.some((source) => sourceKey(source) === sourceKey(destinationRef.current!))) return;
		if (next) { destinationRef.current = next; setDestination(next); }
	}, [sourceResolutionComplete, preferenceLoaded, routeKind, routeSourceId, routeHostId, availableSources, savedPreference?.source, cloudScopeKey]);

	// A project prefill belongs only to the source named by its route.
	useEffect(() => {
		if (!preferenceLoaded) return;
		const savedProject = savedPreference && (savedPreference.source.kind !== "cloud" || savedPreference.scopeKey === cloudScopeKey) && destination && sourceKey(savedPreference.source) === sourceKey(destination)
			? savedPreference.projectId : null;
		const nextProjectId = resolveSpawnProject(
			projectId,
			routeDestination && routeDestination.kind !== "invalid" && destination && sourceKey(routeDestination) === sourceKey(destination) ? routeProjectId : undefined,
			"all",
			projects,
			projectsKnown,
			savedProject,
		);
		if (nextProjectId !== projectId) changeProject(nextProjectId);
	}, [destination, preferenceLoaded, savedPreference, projects, projectsKnown, projectId, routeProjectId, routeKind, routeSourceId, cloudScopeKey]);

	useEffect(() => {
		let cancelled = false;
		const generation = ++catalogGeneration.current;
		const requested = destination;
		setLoading(true);
		setCatalogError(null);
		if (!requested || !sourceFor(requested)) { setCatalog(null); setChatHarnesses([]); setLoading(false); return; }
		if (requested.kind === "cloud") {
			setMode("chat");
			setAttachments([]);
			if (!cloudOrgId) {
				setCatalog(null);
				setChatHarnesses([]);
				setCatalogError("Your Cloud workspace is not ready yet.");
				setLoading(false);
				return;
			}
			void cloudClient.listUserProviderConnections().then((available) => {
				if (cancelled || !spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current)) return;
				const harnesses = readyHarnesses(available);
				const agents = harnesses.map(cloudAgentInfo);
				setCatalog({ supported: agents, installed: agents, authorized: agents });
				setChatHarnesses(harnesses.filter((harness) => harness !== "opencode"));
				setCatalogError(null);
			}).catch((cause) => {
				if (cancelled || !spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current)) return;
				setCatalog(null);
				setChatHarnesses([]);
				setCatalogError(cause instanceof Error ? cause.message : "Could not load Cloud agents.");
			}).finally(() => {
				if (!cancelled && spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current)) setLoading(false);
			});
			return () => { cancelled = true; };
		}
		if (!config) {
			setCatalog(null);
			setChatHarnesses([]);
			setLoading(false);
			return;
		}
		Promise.all([getAgents(config), getSettings(config)])
			.then(([c, settings]) => {
				if (cancelled || !spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current)) return;
				setCatalog(c);
				setChatHarnesses(settings.chatHarnesses);
				setCatalogError(null);
			})
			.catch((e) => {
				// Previously swallowed into `catalog = null`, which left an empty
				// picker and no way to tell the daemon was unreachable.
				if (!cancelled && spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current)) setCatalogError(agentErrorCopy(e));
			})
			.finally(() => {
				if (!cancelled && spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current)) setLoading(false);
			});
		return () => {
			cancelled = true;
		};
	}, [destination, config, cloudClient, cloudOrgId, reloadKey, sourceFor]);

	// Refreshing the catalog moved into the agent sheet route, which owns its own
	// copy of it — see app/sheets/agent.tsx.
	const allAgents = useMemo(() => rankAgents(catalog), [catalog]);
	const agents = useMemo(() => !cloudSpawn && mode === "chat" ? allAgents.filter((agent) => chatHarnesses.includes(agent.id)) : allAgents, [allAgents, chatHarnesses, mode, cloudSpawn]);
	const projectWorkerAgent = projectDetail?.config?.worker?.agent ?? projectDetail?.agent ?? "";
	const projectWorkerModel = projectDetail?.config?.worker?.agentConfig?.model ?? projectDetail?.config?.agentConfig?.model ?? "";
	const resolvedModel = resolveSpawnModel({ selectedAgent: harness, projectWorkerAgent, projectWorkerModel });
	const displayedModel = modelTouched ? model : resolvedModel;
	// "Automatic" when the project pins nothing, because that is the truth: the
	// provider picks, and naming a model here promised one the session never ran.
	const displayedModelLabel = displayedModel ? modelCatalog?.models.find((item) => item.id === displayedModel)?.label ?? displayedModel : "Automatic";
	const modelSelection = modelTouched ? model : "__auto__";
	const cloudDefaults = cloudRoleDefaults(projectDetail?.config as Record<string, unknown> ?? {}, "worker", harness);
	const modelOptions = cloudSpawn
		? [...new Map([...(resolvedModel ? [{ id: resolvedModel, label: resolvedModel }] : []), ...(modelTouched && model ? [{ id: model, label: model }] : []), { id: "__custom__", label: "Custom model…" }].map((option) => [option.id, option])).values()]
		: modelCatalog?.models.map((item) => ({ id: item.id, label: item.label })) ?? [];
	const sourceNotices = !destination && sourceResolutionComplete
		? [availableSources.length === 0 ? "Pair a desktop or sign in to Cloud to start a task." : "Choose where to run this task."]
		: [];
	const notices = [...sourceNotices, ...(cloudSpawn
		? [...new Set([
			mode === "chat" && !loading && agents.length === 0 ? "Connect a Cloud coding agent before starting a worker." : null,
			catalogError,
			modelError ?? null,
		].filter((notice): notice is string => Boolean(notice)))]
		: destination?.kind === "local" ? spawnNotices({
			// Only when a reconnect can fix it: a rejected password stops the poll for
			// good, and its catalog error already says to re-scan the pairing code.
			offline: unreachable,
			mode,
			loading,
			catalogLoaded: catalog !== null,
			catalogError,
			agentCount: agents.length,
			modelError,
		}) : [])];
	const hasComposerMessage = Boolean(
		notices.length > 0
		|| attachmentError
		|| (Platform.OS === "android" && listening)
		|| voice.error
		|| error
		|| offerTUI
		|| reservedWorker
		|| customModelOpen,
	);

	useEffect(() => {
		if (!destination || !projectId || projectId === STANDALONE_PROJECT || (destination.kind === "local" && !config)) { setProjectDetail(undefined); setProjectDetailLoadedFor(projectId); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		setProjectDetailLoadedFor(null);
		const request = destination.kind === "cloud" && cloudOrgId
			? cloudClient.getProject(cloudOrgId, projectId).then(({ project }) => ({ id: project.id, name: project.displayName, kind: "single_repo", config: project.config } as ProjectDetail))
			: getProject(config!, projectId);
		request
			.then((nextProject) => { if (!cancelled && generation === catalogGeneration.current) setProjectDetail(nextProject); })
			.catch((cause) => { if (!cancelled && generation === catalogGeneration.current) setModelError(userFacingError(cause)); })
			.finally(() => { if (!cancelled && generation === catalogGeneration.current) setProjectDetailLoadedFor(projectId); });
		return () => { cancelled = true; };
	}, [destination, config, projectId, reloadKey, cloudClient, cloudOrgId, cloudScopeKey]);

	useEffect(() => {
		if (agentTouched || loading || !catalog) return;
		if (projectId && projectId !== STANDALONE_PROJECT && projectDetailLoadedFor !== projectId) return;
		const nextHarness = resolveSpawnAgent({
			projectWorkerAgent: projectDetail?.config?.worker?.agent,
			projectAgent: projectDetail?.agent,
			availableAgents: agents.filter((agent) => agent.selectable).map((agent) => agent.id),
		});
		setHarness((current) => current === nextHarness ? current : nextHarness);
	}, [agentTouched, agents, catalog, destination, loading, projectDetail, projectDetailLoadedFor, projectId]);

	useEffect(() => {
		if (destination?.kind !== "local" || !config || !projectId || !harness) { setModelCatalog(undefined); setModelLoading(false); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		setModelLoading(true);
		getAgentModels(config, harness, projectId === STANDALONE_PROJECT ? undefined : projectId)
			.then((nextCatalog) => { if (!cancelled && generation === catalogGeneration.current) { setModelCatalog(nextCatalog); setModelError(nextCatalog.warning); } })
			.catch((cause) => { if (!cancelled && generation === catalogGeneration.current) setModelError(userFacingError(cause)); })
			.finally(() => { if (!cancelled && generation === catalogGeneration.current) setModelLoading(false); });
		return () => { cancelled = true; };
	}, [destination, config, harness, projectId, reloadKey]);

	// Loads that failed while the desktop was unreachable run again once the
	// board's poll reconnects. Keyed on the reconnect, not on the errors, so an
	// endpoint that keeps failing while connected can't loop.
	const previousConnection = useRef(connection);
	useEffect(() => {
		const reconnected = previousConnection.current !== "open" && connection === "open";
		previousConnection.current = connection;
		if (destination?.kind === "local" && reconnected && (catalogError || modelError)) setReloadKey((key) => key + 1);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [connection, destination]);
	const selectDestination = (next: SourceRef) => {
		if (destination && sourceKey(destination) === sourceKey(next)) return;
		catalogGeneration.current += 1;
		destinationRef.current = next;
		setDestination(next);
		const preference = { source: next, projectId: null, ...(next.kind === "cloud" ? { scopeKey: cloudScopeKey } : {}) };
		setSavedPreference(preference);
		void saveSpawnPreference(preference);
		setProjectId(null);
		setHarness("");
		setAgentTouched(false);
		setMode("chat");
		setCatalog(null);
		setCatalogError(null);
		setChatHarnesses([]);
		setProjectDetail(undefined);
		setProjectDetailLoadedFor(null);
		setModel("");
		setModelTouched(false);
		setCustomModelOpen(false); setReservedWorker(null);
		setModelCatalog(undefined);
		setModelError(undefined);
		attachmentsRef.current = [];
		setAttachments([]);
		setAttachmentError(undefined);
		setError(null);
		setOfferTUI(false);
		setModelLoading(false);
		requestRef.current = undefined;
		if (next.kind === "local") {
			void switchHost(next.id).catch(() => setError("Could not make that machine the default. Try again."));
		}
	};

	const clearModelOverride = () => { setModel(""); setModelTouched(false); };
	const resetModelSource = () => { clearModelOverride(); setModelCatalog(undefined); setModelError(undefined); setCustomModelOpen(false); };
	const changeProject = (nextProjectId: string | null) => {
		if (!spawnModelSourceChanged({ projectId, agentId: harness }, { projectId: nextProjectId, agentId: harness })) return;
		resetModelSource();
		setProjectDetail(undefined);
		setProjectDetailLoadedFor(null);
		setAgentTouched(false);
		setProjectId(nextProjectId);
		if (destinationRef.current) {
			const preference = { source: destinationRef.current, projectId: nextProjectId, ...(destinationRef.current.kind === "cloud" ? { scopeKey: cloudScopeKey } : {}) };
			setSavedPreference(preference);
			void saveSpawnPreference(preference);
		}
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
		if (nextModel === "__custom__") { setCustomModel(modelTouched ? model : ""); setCustomModelOpen(true); return; }
		if (nextModel === "__auto__") {
			clearModelOverride();
			return;
		}
		setModel(nextModel);
		setModelTouched(true);
	};
	const voiceFeedback = listening ? (
		<View style={styles.voice}>
			<Feather name="mic" size={iconSize.xs} color={t.red} />
			<Text numberOfLines={2} style={styles.voiceText}>
				{voice.partial || (voice.state === "starting" ? "Keep holding…" : "Listening…")}
			</Text>
		</View>
	) : null;
	const pickAttachments = async () => {
		if (pickingAttachments.current) return;
		pickingAttachments.current = true;
		const generation = catalogGeneration.current;
		setAttachmentError(undefined);
		try {
			const result = await DocumentPicker.getDocumentAsync({
				multiple: true,
				copyToCacheDirectory: true,
				type: "*/*",
			});
			if (result.canceled) return;
			const picked = result.assets.map((asset) => {
				const file = new File(asset.uri);
				return {
					name: asset.name,
					mimeType: asset.mimeType || "application/octet-stream",
					bytes: asset.size ?? file.size,
					readData: () => file.base64(),
				};
			});
			const before = attachmentsRef.current;
			const next = await readSpawnAttachments(before, picked);
			if (generation !== catalogGeneration.current) return;
			const merged = appendSpawnAttachments(attachmentsRef.current, next.attachments.slice(before.length));
			attachmentsRef.current = merged.attachments;
			setAttachments(merged.attachments);
			setAttachmentError(next.error ?? merged.error);
		} catch (cause) {
			if (generation === catalogGeneration.current) setAttachmentError(userFacingError(cause, "Couldn't read that file."));
		} finally {
			pickingAttachments.current = false;

		}
	};

	const openWorker = (session: { id: string; projectId: string }, requested: SourceRef, generation: number) => {
		if (!mounted.current || !spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current) || (requested.kind === "cloud" && !cloudAuth.isCurrentSession())) return;
		haptics.success(); backOr(router);
		InteractionManager.runAfterInteractions(() => {
			if (!spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current) || (requested.kind === "cloud" && !cloudAuth.isCurrentSession())) return;
			router.push({ pathname: "/session/[id]", params: { id: session.id, projectId: session.projectId, source: requested.kind, sourceId: requested.id, ...(requested.kind === "cloud" ? { startup: "spawn" } : {}) } });
			if (requested.kind === "cloud" && cloudScopeKey && requestRef.current) {
				void clearCloudOperation(cloudScopeKey, `worker-create/${session.projectId}`, requestRef.current.id).catch(() => {});
			}
		});
	};
	const discardWorkerRetry = () => {
		if (!reservedWorker || !projectId) return;
		const operation = reservedWorker;
		Alert.alert("Discard retry record?", "This does not delete a worker. Check existing workers first if the request may have completed.", [
			{ text: "Keep record", style: "cancel" },
			{ text: "Discard record", style: "destructive", onPress: () => {
				void clearCloudOperation(operation.scopeKey, `worker-create/${projectId}`, operation.key).then(() => {
					if (!mounted.current || !cloudAuth.isCurrentSession()) return;
					setReservedWorker(null); requestRef.current = undefined; setError(null);
				}).catch((cause) => { if (mounted.current) setError(userFacingError(cause)); });
			} },
		]);
	};
	const onSpawn = async () => {
		if (submitting.current || operationLoading || !mounted.current) return;
		if (!canSubmitSpawn(destination, projectId, harness, sourceFor) || !destination) return;
		if (cloudSpawn && !cloudAuth.isCurrentSession()) return;
		if (cloudSpawn && (projectDetailLoadedFor !== projectId || modelError)) return;
		if (reservedWorker?.acceptedId && projectId) { openWorker({ id: reservedWorker.acceptedId, projectId }, destination, catalogGeneration.current); return; }
		if (pickingAttachments.current) {
			setAttachmentError("Wait for attachments to finish loading.");
			return;
		}
		// Validated on submit rather than by disabling the button — desktop's
		// choice, and the better one: a disabled button with no explanation is
		// worse than a message naming what is missing.
		submitting.current = true;
		const generation = catalogGeneration.current;
		const requested = destination;
		setBusy(true);
		setError(null);
		setOfferTUI(false);
		try {
			const request = {
				projectId: projectId ?? undefined,
				prompt: prompt.trim() || undefined,
				harness: harness || undefined,
				model: cloudSpawn ? displayedModel || undefined : modelOverride(displayedModel, modelTouched),
				...(cloudSpawn ? { reasoningEffort: reservedWorker ? reservedWorker.payload.reasoningEffort : cloudDefaults.reasoningEffort } : {}),
				mode,
				attachments: attachmentsRef.current.map(({ mimeType, data }) => ({ mimeType, data })),
			};
			const payload = spawnRequestFingerprint(cloudSpawn ? cloudScopeKey ?? "" : "", `${destination.kind}/${destination.id}`, request);
			if (reservedWorker && requestRef.current?.payload !== payload) throw new Error("Resolve the previous Cloud request before starting a changed request. Check workers or discard its retry record.");
			if (requestRef.current?.payload !== payload || requestRef.current?.attachments !== attachmentsRef.current) {
				requestRef.current = { payload, attachments: attachmentsRef.current, id: `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}` };
			}
			const session = await spawnOn(destination, { ...request, clientRequestId: requestRef.current.id });
			if (!mounted.current || !spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current) || (cloudSpawn && !cloudAuth.isCurrentSession())) return;
			if (projectId === STANDALONE_PROJECT) setActiveProject(ALL_PROJECTS);
			openWorker(session, requested, generation);
		} catch (e) {
			if (!mounted.current || !spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current) || (cloudSpawn && !cloudAuth.isCurrentSession())) return;
			haptics.error();
			setError(spawnErrorCopy(e, destination.kind));
			if (cloudSpawn && cloudScopeKey && projectId) {
				try { setReservedWorker(await readCloudOperation(cloudScopeKey, `worker-create/${projectId}`)); } catch {}
			}
			setOfferTUI(!cloudSpawn && mode === "chat" && isChatPreflightError(e));
		} finally { submitting.current = false; if (mounted.current && spawnRequestIsCurrent(generation, catalogGeneration.current, requested, destinationRef.current)) setBusy(false); }
	};

	const content = (
		<View style={[
			styles.content,
			Platform.OS === "ios" && styles.iosContent,
			Platform.OS === "android" && styles.androidContent,
		]}>
				<View
					style={[styles.promptHost, Platform.OS === "ios" && styles.promptHostFill]}
					onLayout={Platform.OS === "ios" ? (event) => setPromptRoom(Math.floor(event.nativeEvent.layout.height)) : undefined}
				>
					<SpawnPromptInput value={prompt} onChangeText={setPrompt} height={Platform.OS === "ios" ? promptRoom : undefined} />
				</View>

				{attachments.length ? (
					<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.attachments}>
						{attachments.map((item, index) => (
							<View key={`${item.name}-${index}`} style={styles.attachment}>
								<Feather name="file-text" size={iconSize.sm} color={t.accent} />
								<Text numberOfLines={1} style={styles.attachmentName}>{item.name}</Text>
								<Pressable
									hitSlop={8}
									accessibilityLabel={`Remove ${item.name}`}
									onPress={() => {
										attachmentsRef.current = attachmentsRef.current.filter((candidate) => candidate !== item);
										setAttachments(attachmentsRef.current);
									}}
								>
									<Feather name="x" size={iconSize.xs} color={t.textTertiary} />
								</Pressable>
							</View>
						))}
					</ScrollView>
				) : null}

		{hasComposerMessage ? <View style={styles.messages}>
					{cloudSpawn && (catalogError || modelError) ? <Button title="Retry setup" variant="ghost" onPress={() => { setModelError(undefined); setReloadKey((key) => key + 1); }} /> : null}
					{reservedWorker ? <View>
						<Text style={styles.warn}>{reservedWorker.acceptedId ? "Worker created. Open it without starting another." : "A previous request may have completed. Retry with the same request or check workers."}</Text>
						{reservedWorker.acceptedId && destination && projectId ? <Button title="Open worker" onPress={() => openWorker({ id: reservedWorker.acceptedId!, projectId }, destination, catalogGeneration.current)} /> : null}
						<Button title="Check workers" variant="ghost" onPress={() => backOr(router, "/workers")} />
						<Button title="Discard retry record" variant="ghost" onPress={discardWorkerRetry} />
					</View> : null}
					{customModelOpen ? <View>
						<Text style={styles.warn}>Enter a model ID supported by this Cloud agent.</Text>
						<TextInput {...textSelectionColors(t)} accessibilityLabel="Custom model ID" value={customModel} onChangeText={setCustomModel} autoCapitalize="none" autoCorrect={false} returnKeyType="done" style={[styles.customModel, { color: t.textPrimary, backgroundColor: t.bgElevated }]} />
						<Button title="Use model" disabled={!customModel.trim()} onPress={() => { selectModel(customModel.trim()); setCustomModelOpen(false); }} />
						<Button title="Cancel model entry" variant="ghost" onPress={() => setCustomModelOpen(false)} />
					</View> : null}
					{notices.map((notice) => <Text key={notice} style={styles.warn}>{notice}</Text>)}
					{attachmentError ? <Text style={styles.warn}>{attachmentError}</Text> : null}
					{Platform.OS === "android" ? voiceFeedback : null}
					{voice.error ? <Text accessibilityRole="alert" style={styles.warn}>{voice.error}</Text> : null}
					{error ? <Text style={styles.error}>{error}</Text> : null}
					{offerTUI ? <Button title="Create as Terminal UI instead" variant="ghost" icon="terminal" onPress={() => { selectMode("tui"); setOfferTUI(false); setError(null); }} /> : null}
				</View> : null}

				{/* The sticky controls move visually but keep their original layout
				    position. Reserve that movement before them so chips and messages
				    remain visible above the keyboard, not behind the controls. */}
				{Platform.OS === "ios" && keyboardHeight > 0 ? (
					<View pointerEvents="none" style={{ height: keyboardHeight, marginTop: -space.sm }} />
				) : null}

				{/* The controls ride the keyboard on the UI thread.
				    iOS does not lift this form sheet for the IME, and every
				    height-based attempt moved late or not at all: a settled keyboard
				    height only lands after the animation, animated padding is
				    interpolated on the JS thread, and a keyboard-avoiding wrapper
				    mismeasures its own frame inside a sheet, leaving Start task behind
				    the keyboard. A sticky view translates by the live offset, so
				    the selectors and the button sit directly above it. */}
				<KeyboardStickyView offset={{ closed: 0, opened: 0 }}>
				{Platform.OS === "ios" ? voiceFeedback : null}
				<SpawnComposerControls
					destinations={destinations}
					destination={destination}
					onSelectDestination={selectDestination}
					projects={[...projects.map((item) => ({ id: item.id, label: item.name, icon: "folder" as const })), ...(destination?.kind === "local" ? [{ id: STANDALONE_PROJECT, label: "Standalone agent", icon: "message-square-plus" as const, sectionBreakBefore: true }] : [])]}
					projectId={projectId}
					onSelectProject={changeProject}
					agents={agents.filter((item) => item.selectable).map((item) => ({ id: item.id, label: item.label }))}
					harness={harness}
					onSelectHarness={selectAgent}
					models={modelOptions}
					modelSelection={modelSelection}
					modelLabel={displayedModelLabel}
					onSelectModel={selectModel}
					onAttach={() => { void pickAttachments(); }}
					showAttachments={!cloudSpawn}
					showModels={true}
					voice={{ state: voice.state, mode: voice.mode, onPressIn: voice.pressIn, onPressOut: voice.pressOut }}
					onSpawn={() => { void onSpawn(); }}
					busy={busy}
					disabled={!canSubmitSpawn(destination, projectId, harness, sourceFor) || busy || modelLoading || loading || listening || voice.state === "transcribing" || operationLoading || (cloudSpawn && (projectDetailLoadedFor !== projectId || !!modelError))}
				/>
				</KeyboardStickyView>
		</View>
	);

	if (Platform.OS === "android") {
		return (
			<View style={styles.androidModalRoot}>
				<BottomSheet
					index={0}
					enablePanDownToClose
					enableDynamicSizing
					backgroundStyle={{ backgroundColor: t.bgBase }}
					onClose={() => backOr(router)}
				>
					<BottomSheetView style={styles.androidSheet}>
						{content}
					</BottomSheetView>
				</BottomSheet>
			</View>
		);
	}

	return <View style={styles.screen}>{content}</View>;
}

// Human copy for a failed spawn, matching every other screen. This one used to
// render `e.message` — the wire string, e.g. "401 - missing or invalid
// connection password".
function spawnErrorCopy(e: unknown, environment: "local" | "cloud" | null): string {
	if (isChatPreflightError(e)) return chatErrorCopy(e);
	if (e instanceof ApiError && e.code === "PROMPT_TOO_LONG") {
		return "Task prompt is too long. Keep it to 16 KiB or fewer (emoji and other non-English characters use more than one byte). Shorten it and try again.";
	}
	if (environment === "cloud") return cloudRequestErrorCopy(e);
	return userFacingError(e, "Couldn't start the worker. Try again.");
}

const CLOUD_AGENT_LABELS: Record<string, string> = {
	"claude-code": "Claude Code",
	codex: "Codex",
	cursor: "Cursor",
	opencode: "OpenCode",
};

function cloudAgentInfo(id: string): AgentInfo {
	return { id, label: CLOUD_AGENT_LABELS[id] ?? id, authStatus: "authorized" };
}

// Android's compact field height and the iOS host's minimum layout height.
const PROMPT_MIN_HEIGHT = 112;

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		content: { flex: 1, paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.sm, gap: space.sm },
		iosContent: { paddingTop: space.xxxl },
		androidModalRoot: { flex: 1, backgroundColor: "transparent" },
		androidSheet: {
			paddingTop: space.xs,
			paddingBottom: space.md,
			backgroundColor: t.bgBase,
		},
		androidContent: { flex: 0, paddingTop: space.md, paddingBottom: space.none },
		messages: { gap: space.xs },
		customModel: { minHeight: 44, borderRadius: 12, paddingHorizontal: space.md, fontSize: type.body.fontSize },
		voice: { flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.tintRed, borderRadius: 8, paddingHorizontal: space.sm, paddingVertical: space.xs },
		voiceText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.caption2.fontSize },
		promptHost: { width: "100%", height: PROMPT_MIN_HEIGHT },
		promptHostFill: { height: undefined, flex: 1, minHeight: 0 },
		attachments: { gap: space.sm },
		attachment: { maxWidth: 190, height: 36, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.sm, borderRadius: 12, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
		attachmentName: { fontFamily: "Geist_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.caption1.fontSize },
		warn: { fontFamily: "Geist_400Regular", color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		error: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
