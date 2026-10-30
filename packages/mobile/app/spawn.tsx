import { useLocalSearchParams, useRouter } from "expo-router";
import { Feather } from "../lib/icons";
import BottomSheet, { BottomSheetView } from "@expo/ui/community/bottom-sheet";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { File } from "expo-file-system";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	InteractionManager,
	Platform,
	Pressable,
	StyleSheet,
	Text,
	View,
} from "react-native";
import { KeyboardStickyView, useKeyboardState } from "react-native-keyboard-controller";
import { agentErrorCopy } from "../lib/agentError";
import { defaultAgent, rankAgents } from "../lib/agentPicker";
import { ApiError, getAgentModels, getAgents, getProject, getSettings, type AgentCatalog, type AgentModelCatalog, type ProjectDetail, type SessionMode } from "../lib/api";
import { userFacingError } from "../lib/connectionError";
import { chatErrorCopy, isChatPreflightError } from "../lib/chatError";
import { haptics } from "../lib/haptics";
import { connectedSpawnHosts, initialSpawnHostId, selectSpawnHost, spawnHostRequestIsCurrent } from "../lib/hostRoute";
import { ALL_PROJECTS, STANDALONE_PROJECT, resolveSpawnProject } from "../lib/projectFilter";
import { modelOverride, resolveSpawnAgent, resolveSpawnModel, spawnModelSourceChanged } from "../lib/spawnModel";
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
import { iconSize, space, type } from "../lib/tokens";
import { backOr } from "../lib/backNavigation";

export { SheetErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

export default function SpawnModal() {
	return <SpawnModalContent />;
}

function SpawnModalContent() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
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
	// "Automatic" when the project pins nothing, because that is the truth: the
	// provider picks, and naming a model here promised one the session never ran.
	const displayedModelLabel = displayedModel ? modelCatalog?.models.find((item) => item.id === displayedModel)?.label ?? displayedModel : "Automatic";
	const modelSelection = modelTouched ? model : "__auto__";
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

	useEffect(() => {
		const requestedHostId = selectedHostId;
		if (!config || !requestedHostId || !projectId || !harness) { setModelCatalog(undefined); setModelLoading(false); return; }
		let cancelled = false;
		const generation = catalogGeneration.current;
		setModelLoading(true);
		const polling = new AbortController();
		getAgentModels(config, harness, projectId === STANDALONE_PROJECT ? undefined : projectId, polling.signal)
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
	const voiceFeedback = listening ? (
		<View style={styles.voice}>
			<Feather name="mic" size={iconSize.xs} color={t.red} />
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
					hosts={hostOptions}
					hostId={selectedHostId}
					onSelectHost={chooseHost}
					projects={[...projects.map((item) => ({ id: item.id, label: item.name, icon: "folder" as const })), { id: STANDALONE_PROJECT, label: "Standalone agent", icon: "message-square-plus" as const, sectionBreakBefore: true }]}
					projectId={projectId}
					onSelectProject={changeProject}
					agents={agents.filter((item) => item.selectable).map((item) => ({ id: item.id, label: item.label }))}
					harness={harness}
					onSelectHarness={selectAgent}
					models={modelCatalog?.models.map((item) => ({ id: item.id, label: item.label })) ?? []}
					modelSelection={modelSelection}
					modelLabel={displayedModelLabel}
					onSelectModel={selectModel}
					onChoosePhoto={() => { void pickPhotos(); }}
					onChooseFile={() => { void pickFiles(); }}
					voice={{ state: voice.state, mode: voice.mode, onPressIn: voice.pressIn, onPressOut: voice.pressOut }}
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
		voice: { flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.tintRed, borderRadius: 8, paddingHorizontal: space.sm, paddingVertical: space.xs },
		voiceText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.caption2.fontSize },
		promptHost: { width: "100%", height: PROMPT_MIN_HEIGHT },
		promptHostFill: { height: undefined, flex: 1, minHeight: 0 },
		warn: { fontFamily: "Geist_400Regular", color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		error: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
