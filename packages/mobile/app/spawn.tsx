import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import BottomSheet, { BottomSheetView } from "@expo/ui/community/bottom-sheet";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { File } from "expo-file-system";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	InteractionManager,
	Platform,
	StyleSheet,
	Text,
	View,
} from "react-native";
import { KeyboardStickyView, useKeyboardState } from "react-native-keyboard-controller";
import { useHeaderOverlap } from "../lib/useHeaderOverlap";
import { agentErrorCopy } from "../lib/agentError";
import { AppSymbol } from "../lib/AppSymbol";
import { defaultAgent, rankAgents } from "../lib/agentPicker";
import { ApiError, getAgentModels, getAgents, getProject, getSettings, type AgentCatalog, type AgentModelCatalog, type AgentModelInfo, type ProjectDetail, type SessionMode } from "../lib/api";
import { userFacingError } from "../lib/connectionError";
import { chatErrorCopy, isChatPreflightError } from "../lib/chatError";
import { haptics } from "../lib/haptics";
import { connectedSpawnHosts, initialSpawnHostId, selectSpawnHost, spawnHostRequestIsCurrent } from "../lib/hostRoute";
import { ALL_PROJECTS, STANDALONE_PROJECT, resolveSpawnProject } from "../lib/projectFilter";
import { menuModels, modelOverride, resolveSpawnAgent, resolveSpawnModel, spawnModelSourceChanged } from "../lib/spawnModel";
import { appendSpawnAttachments, MAX_SPAWN_ATTACHMENTS, photoPickerAttachment, readSpawnAttachments, type PickedSpawnAttachment, type SpawnAttachment } from "../lib/spawn-attachments";
import { SpawnAttachmentRow } from "../lib/spawn-attachment-row";
import { SpawnComposerControls } from "../lib/spawn-composer-controls";
import { spawnNotices } from "../lib/spawnNotices";
import { SpawnPromptInput } from "../lib/spawn-prompt-input";
import { useApp } from "../lib/store";
import { useVoiceInput } from "../lib/voice/useVoiceInput";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { Button } from "../lib/ui";
import { iconSize, radius, space, type } from "../lib/tokens";
import { backOr } from "../lib/backNavigation";
import { approvalOptions, resolveApprovalMode, type ApprovalMode } from "../lib/spawnApproval";
import { modelSheetRoute } from "../lib/sheetResult";

export { SheetErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

export default function SpawnModal() {
	return <SpawnModalContent />;
}

function SpawnModalContent() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const navigation = useNavigation();
	const { projectId: routeProjectId, hostId: routeHostId } = useLocalSearchParams<{ projectId?: string; hostId?: string }>();
	const { hostStates, activeProjectId, currentHostId, configForHost, switchHost, setActiveProject, spawn } = useApp();
	const connectedHosts = useMemo(() => connectedSpawnHosts(hostStates), [hostStates]);
	const [selectedHostId, setSelectedHostId] = useState<string | null>(null);
	const hostSelectionInitialized = useRef(false);
	const catalogGeneration = useRef(0);
	const selectedHost = hostStates.find((host) => host.hostId === selectedHostId);
	const config = selectedHostId ? configForHost(selectedHostId) : null;
	const projects = selectedHost?.projects ?? [];
	const projectsKnown = selectedHost?.projectsKnown ?? false;
	const connection = selectedHost?.connection ?? "closed";
	const unreachable = Boolean(selectedHost && selectedHost.connection === "closed");
	const hostOptions = useMemo(() => {
		if (!selectedHostId || connectedHosts.some((host) => host.id === selectedHostId)) return connectedHosts;
		const selected = hostStates.find((host) => host.hostId === selectedHostId);
		return selected ? [{ id: selected.hostId, label: selected.name }, ...connectedHosts] : connectedHosts;
	}, [connectedHosts, hostStates, selectedHostId]);

	useEffect(() => {
		if (hostSelectionInitialized.current) return;
		const routed = routeHostId ? hostStates.find((host) => host.hostId === routeHostId) : undefined;
		if ((routeHostId && routed?.loading) || (!routeHostId && connectedHosts.length === 0 && hostStates.some((host) => host.loading))) return;
		hostSelectionInitialized.current = true;
		setSelectedHostId(initialSpawnHostId({ routeHostId, currentHostId: currentHostId ?? undefined, hosts: connectedHosts }));
	}, [connectedHosts, currentHostId, hostStates, routeHostId]);

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
	const [modelCatalog, setModelCatalog] = useState<AgentModelCatalog>();
	// Every agent's own models, so the agent menu can offer each one's list
	// before it is picked. The picked agent's list is the project-aware catalog.
	const [agentModelLists, setAgentModelLists] = useState<Record<string, AgentModelInfo[]>>({});
	const [approvalChoice, setApprovalChoice] = useState<ApprovalMode>();
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
	const headerOverlap = useHeaderOverlap();



	// Seed from this machine's routed project, or the selected machine's active
	// project. A project id is never carried across machines.
	useEffect(() => {
		const nextProjectId = resolveSpawnProject(
			projectId,
			routeHostId === selectedHostId ? routeProjectId : undefined,
			selectedHostId === currentHostId ? activeProjectId : ALL_PROJECTS,
			projects,
			projectsKnown,
		);
		if (nextProjectId !== projectId) changeProject(nextProjectId);
	}, [activeProjectId, currentHostId, projects, projectsKnown, projectId, routeHostId, routeProjectId, selectedHostId]);

	useEffect(() => {
		const requestedHostId = selectedHostId;
		if (!config || !requestedHostId) { setCatalog(null); setChatHarnesses([]); setLoading(false); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		setLoading(true);
		Promise.all([getAgents(config), getSettings(config)])
			.then(([c, settings]) => {
				if (cancelled || !spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) return;
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
	}, [config, reloadKey, selectedHostId]);

	// Refreshing the catalog moved into the agent sheet route, which owns its own
	// copy of it — see app/sheets/agent.tsx.
	const allAgents = useMemo(() => rankAgents(catalog), [catalog]);
	const agents = useMemo(() => mode === "chat" ? allAgents.filter((agent) => chatHarnesses.includes(agent.id)) : allAgents, [allAgents, chatHarnesses, mode]);
	const projectWorkerAgent = projectDetail?.config?.worker?.agent ?? projectDetail?.agent ?? "";
	const projectWorkerModel = projectDetail?.config?.worker?.agentConfig?.model ?? projectDetail?.config?.agentConfig?.model ?? "";
	const resolvedModel = resolveSpawnModel({ selectedAgent: harness, projectWorkerAgent, projectWorkerModel });
	const displayedModel = modelTouched ? model : resolvedModel;
	// The project-aware catalog once it lists something. An empty one (still
	// discovering, or a project that names none) must not hide the machine's
	// list, which is what left the menu offering only the default.
	const currentModels = modelCatalog?.models.length ? modelCatalog.models : agentModelLists[harness] ?? [];
	// "Default model" when the project pins nothing, because that is the truth:
	// the provider picks, and naming a model here promised one the session never
	// ran. Not "Automatic", which reads as an approval mode.
	const displayedModelLabel = displayedModel ? currentModels.find((item) => item.id === displayedModel)?.label ?? displayedModel : "Default model";
	const projectApproval = projectDetail?.config?.worker?.agentConfig?.permissions ?? projectDetail?.config?.agentConfig?.permissions;
	const approvalMode = resolveApprovalMode({ harness, chosen: approvalChoice, projectMode: projectApproval });
	const modelSelection = modelTouched ? model : "__auto__";
	// Each agent's menu list, built once per change rather than per keystroke:
	// some catalogs run to thousands of models.
	const agentMenuModels = useMemo(() => Object.fromEntries(agents.filter((agent) => agent.selectable).map((agent) => [
		agent.id,
		menuModels(agent.id === harness ? currentModels : agentModelLists[agent.id] ?? [], agent.id === harness ? modelSelection : "__auto__"),
	])), [agentModelLists, agents, currentModels, harness, modelSelection]);
	const hostNotices = selectedHostId === null && hostSelectionInitialized.current
		? [routeHostId ? "That machine is not connected." : "No connected machines are available."]
		: [];
	const notices = [...hostNotices, ...spawnNotices({
		// Only when a reconnect can fix it: a rejected password stops the poll for
		// good, and its catalog error already says to re-scan the pairing code.
		offline: unreachable,
		mode,
		loading,
		catalogLoaded: catalog !== null,
		catalogError,
		agentCount: agents.length,
		modelError,
	})];
	const hasComposerMessage = Boolean(
		notices.length > 0
		|| attachmentError
		|| (Platform.OS === "android" && listening)
		|| voice.error
		|| error
		|| offerTUI,
	);

	useEffect(() => {
		const requestedHostId = selectedHostId;
		if (!config || !requestedHostId || !projectId || projectId === STANDALONE_PROJECT) { setProjectDetail(undefined); setProjectDetailLoadedFor(null); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		setProjectDetailLoadedFor(null);
		getProject(config, projectId)
			.then((nextProject) => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setProjectDetail(nextProject); })
			.catch((cause) => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setModelError(userFacingError(cause)); })
			.finally(() => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setProjectDetailLoadedFor(projectId); });
		return () => { cancelled = true; };
	}, [config, projectId, reloadKey, selectedHostId]);

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
	useEffect(() => {
		const requestedHostId = selectedHostId;
		if (!config || !requestedHostId || !agentIds) { setAgentModelLists({}); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		const polling = new AbortController();
		void Promise.all(agentIds.split("|").map(async (id) => {
			try {
				return [id, (await getAgentModels(config, id, undefined, polling.signal)).models ?? []] as const;
			} catch {
				return [id, []] as const;
			}
		})).then((entries) => {
			if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setAgentModelLists(Object.fromEntries(entries));
		});
		return () => { cancelled = true; polling.abort(); };
	}, [agentIds, config, reloadKey, selectedHostId]);

	useEffect(() => {
		const requestedHostId = selectedHostId;
		// The machine's own catalog loads before a project is chosen, so the
		// model menu is never just the default; choosing a project swaps in its.
		if (!config || !requestedHostId || !harness) { setModelCatalog(undefined); setModelLoading(false); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		setModelLoading(true);
		const polling = new AbortController();
		getAgentModels(config, harness, !projectId || projectId === STANDALONE_PROJECT ? undefined : projectId, polling.signal)
			.then((nextCatalog) => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) { setModelCatalog(nextCatalog); setModelError(nextCatalog.warning); } })
			.catch((cause) => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setModelError(userFacingError(cause)); })
			.finally(() => { if (!cancelled && spawnHostRequestIsCurrent(generation, catalogGeneration.current, requestedHostId, selectedHostId)) setModelLoading(false); });
		return () => { cancelled = true; polling.abort(); };
	}, [config, harness, projectId, reloadKey, selectedHostId]);

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

	const clearModelOverride = () => { setModel(""); setModelTouched(false); };
	const resetModelSource = () => { clearModelOverride(); setModelCatalog(undefined); setModelError(undefined); };
	const chooseHost = (nextHostId: string) => {
		if (nextHostId === selectedHostId) return;
		catalogGeneration.current += 1;
		setProjectId(null);
		setHarness("");
		setAgentTouched(false);
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
		void selectSpawnHost(nextHostId, { select: setSelectedHostId, persistDefault: switchHost })
			.catch(() => setError("Could not make that machine the default. Try again."));
	};
	const changeProject = (nextProjectId: string | null) => {
		if (!spawnModelSourceChanged({ projectId, agentId: harness }, { projectId: nextProjectId, agentId: harness })) return;
		resetModelSource();
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
		setModel(nextModel);
		setModelTouched(true);
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
			// The sheet's default row answers "".
			onSelect: (id) => selectAgentModel(agentId, id || "__auto__"),
		}));
	};
	const voiceFeedback = listening ? (
		<View style={styles.voice}>
			<AppSymbol name="mic" size={iconSize.sm} color={t.red} />
			<Text numberOfLines={2} style={styles.voiceText}>
				{voice.partial || (voice.state === "starting" ? "Keep holding…" : "Listening…")}
			</Text>
		</View>
	) : null;
	const addAttachments = async (pick: () => Promise<PickedSpawnAttachment[] | null>, fallback: string) => {
		if (pickingAttachments.current) return;
		pickingAttachments.current = true;
		setAttachmentError(undefined);
		try {
			const picked = await pick();
			if (!picked) return;
			const before = attachmentsRef.current;
			const next = await readSpawnAttachments(before, picked);
			const merged = appendSpawnAttachments(attachmentsRef.current, next.attachments.slice(before.length));
			attachmentsRef.current = merged.attachments;
			setAttachments(merged.attachments);
			setAttachmentError(next.error ?? merged.error);
		} catch (cause) {
			setAttachmentError(userFacingError(cause, fallback));
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

	const onSpawn = async () => {
		if (!selectedHostId || !config) {
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
		setError(null);
		setOfferTUI(false);
		try {
			const request = {
				hostId: selectedHostId,
				projectId: projectId ?? undefined,
				prompt: prompt.trim() || undefined,
				harness: harness || undefined,
				model: modelOverride(displayedModel, modelTouched),
				// Only a mode picked here overrides the project's; an untouched
				// picker names the project's mode, which the daemon applies anyway.
				approvalMode: approvalChoice === approvalMode ? approvalChoice : undefined,
				mode,
				attachments: attachmentsRef.current.map(({ mimeType, data }) => ({ mimeType, data })),
			};
			const { attachments: _, ...requestFields } = request;
			const payload = JSON.stringify(requestFields);
			if (requestRef.current?.payload !== payload || requestRef.current?.attachments !== attachmentsRef.current) {
				requestRef.current = { payload, attachments: attachmentsRef.current, id: `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}` };
			}
			const session = await spawn({ ...request, clientRequestId: requestRef.current.id });
			if (projectId === STANDALONE_PROJECT) setActiveProject(ALL_PROJECTS);
			// Left while the request ran (a notification or link can still
			// navigate away): the worker shows up on the board, and closing the
			// sheet again would pop the page that was under it. The sheet lets go
			// again, or coming back to it would find it locked for good; a second
			// Start reuses the request id, so it can't spawn a second worker.
			if (!navigation.isFocused()) {
				setBusy(false);
				return;
			}
			haptics.success();
			// Dismiss the modal first, then open the freshly spawned session's mode-aware surface
			// once the dismiss transition has settled. Firing both navigations in the
			// same tick overlaps their animations (the modal retracts while the session
			// is already sliding in); runAfterInteractions waits for the modal's
			// transition to finish so the two happen back-to-back, not on top of each
			// other. The session screen shows its own "connecting" state while the
			// terminal attaches, so landing on it before the PTY is ready is expected.
			backOr(router);
			InteractionManager.runAfterInteractions(() => {
				router.push({
					pathname: "/session/[id]",
					params: { id: session.id, projectId: session.projectId, hostId: selectedHostId },
				});
			});
		} catch (e) {
			haptics.error();
			setError(spawnErrorCopy(e));
			setOfferTUI(mode === "chat" && isChatPreflightError(e));
			setBusy(false);
		}
	};

	// The sheet's bar is empty: a swipe down closes it. A started request can't be
	// called back, so while it runs the sheet can't be swiped away; it closes
	// itself on success or stays to show the error.
	useEffect(() => {
		if (!busy) return;
		navigation.setOptions({ gestureEnabled: false });
		return () => navigation.setOptions({ gestureEnabled: true });
	}, [busy, navigation]);

	const content = (
		<View style={[
			styles.content,
			// The sheet's empty bar is laid over the top of the page, so the
			// prompt starts below it rather than under it.
			Platform.OS === "ios" && { paddingTop: headerOverlap },
			Platform.OS === "android" && styles.androidContent,
		]}>
				<View
					style={[styles.promptHost, Platform.OS === "ios" && styles.promptHostFill]}
					onLayout={Platform.OS === "ios" ? (event) => setPromptRoom(Math.floor(event.nativeEvent.layout.height)) : undefined}
				>
					<SpawnPromptInput value={prompt} onChangeText={setPrompt} height={Platform.OS === "ios" ? promptRoom : undefined} />
				</View>

				<SpawnAttachmentRow
					attachments={attachments}
					onRemove={(item) => {
						attachmentsRef.current = attachmentsRef.current.filter((candidate) => candidate !== item);
						setAttachments(attachmentsRef.current);
					}}
				/>

		{hasComposerMessage ? <View style={styles.messages}>
					{notices.map((notice) => <Text key={notice} style={styles.warn}>{notice}</Text>)}
					{attachmentError ? <Text style={styles.warn}>{attachmentError}</Text> : null}
					{Platform.OS === "android" ? voiceFeedback : null}
					{voice.error ? <Text accessibilityRole="alert" style={styles.warn}>{voice.error}</Text> : null}
					{error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
					{offerTUI ? <Button title="Create as Terminal UI instead" variant="secondary" icon="terminal" onPress={() => { selectMode("tui"); setOfferTUI(false); setError(null); }} /> : null}
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
					hosts={hostOptions}
					hostId={selectedHostId}
					onSelectHost={chooseHost}
					projects={[...projects.map((item) => ({ id: item.id, label: item.name, icon: "folder" as const })), { id: STANDALONE_PROJECT, label: "Standalone agent", icon: "message-square-plus" as const, sectionBreakBefore: true }]}
					projectId={projectId}
					onSelectProject={changeProject}
					agents={agents.filter((item) => item.selectable).map((item) => ({ id: item.id, label: item.label }))}
					harness={harness}
					onSelectHarness={selectAgent}
					models={currentModels.map((item) => ({ id: item.id, label: item.label }))}
					agentModels={agentMenuModels}
					modelSelection={modelSelection}
					modelLabel={displayedModelLabel}
					onSelectModel={selectModel}
					onSelectAgentModel={selectAgentModel}
					usesDefaultModel={!displayedModel}
					onBrowseModels={browseModels}
					approvalModes={approvalOptions(harness)}
					approvalMode={approvalMode}
					onSelectApprovalMode={(next) => setApprovalChoice(next as ApprovalMode)}
					onChoosePhoto={() => { void pickPhotos(); }}
					onChooseFile={() => { void pickFiles(); }}
					voice={{ state: voice.state, mode: voice.mode, onPressIn: voice.pressIn, onPressOut: voice.pressOut, onToggleLatched: voice.toggleLatched }}
					promptEmpty={!prompt.trim()}
					onSpawn={() => { void onSpawn(); }}
					busy={busy}
					disabled={!selectedHostId || !config || !projectId || !harness || busy || modelLoading || loading || listening || voice.state === "transcribing"}
				/>
				</KeyboardStickyView>
		</View>
	);

	if (Platform.OS === "android") {
		return (
			<View style={styles.androidModalRoot}>
				{/* Like the iOS sheet, it holds still while a started request runs:
				    no drag, back press or outside tap can close it mid-spawn. */}
				<BottomSheet
					index={0}
					enablePanDownToClose={!busy}
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

// Human copy for a failed spawn, matching every other screen. Never the wire
// string ("401 Unauthorized - missing or invalid connection password").
function spawnErrorCopy(e: unknown): string {
	if (isChatPreflightError(e)) return chatErrorCopy(e);
	if (e instanceof ApiError && e.code === "PROMPT_TOO_LONG") {
		return "Task prompt is too long. Keep it to 16 KiB or fewer (emoji and other non-English characters use more than one byte). Shorten it and try again.";
	}
	return userFacingError(e, "Couldn't start the worker. Try again.");
}

// Android's compact field height and the iOS host's minimum layout height.
const PROMPT_MIN_HEIGHT = 112;

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// The sheet's raised surface, a step above the board it covers.
		screen: { flex: 1, backgroundColor: t.bgSurface },
		content: { flex: 1, paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.sm, gap: space.sm },
		androidModalRoot: { flex: 1, backgroundColor: "transparent" },
		androidSheet: {
			paddingTop: space.xs,
			paddingBottom: space.md,
			backgroundColor: t.bgBase,
		},
		androidContent: { flex: 0, paddingTop: space.md, paddingBottom: space.none },
		messages: { gap: space.xs },
		voice: { flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.tintRed, borderRadius: radius.sm, paddingHorizontal: space.sm, paddingVertical: space.xs },
		voiceText: { fontFamily: "DMSans_400Regular", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		promptHost: { width: "100%", height: PROMPT_MIN_HEIGHT },
		promptHostFill: { height: undefined, flex: 1, minHeight: 0 },
		warn: { fontFamily: "DMSans_400Regular", color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		error: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
