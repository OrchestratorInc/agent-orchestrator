import { useTranslation } from "react-i18next";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import {
	ArrowUp,
	Loader2,
	MessageSquare,
	MessageSquarePlus,
	Pencil,
	Plus,
	Square,
	X,
} from "lucide-react";
import type { components } from "../../../api/schema";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import type { ChatSkill } from "../../types/conversation";
import type { ChatModel, ConversationActivity, ConversationMessage } from "../../types/conversation";
import { apiClient, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { ActivityRow, ApprovalCard, AssistantMessage, HumanMessage } from "./ChatTimelineItems";
import { ComposerEditor, type ComposerEditorHandle, type ComposerTrigger } from "./ComposerEditor";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";
import { rankSkills, moveHighlight } from "./composerSuggest";
import { ChatAnnotationSummary, type ChatAnnotationSummaryItem } from "./ChatAnnotationSummary";
import { emptySideChatDraft, parseSideChatDraft, type SideChatDraft } from "./sideChatDraft";
import { TurnSettingsBar } from "./TurnSettingsBar";
import { QueuedMessageDock } from "./QueuedMessageDock";
import { Button } from "../ui/button";
import { ConfirmDialog } from "../ConfirmDialog";
import { ContextMenu, ContextMenuTrigger, ContextMenuContent, ContextMenuItem } from "../ui/context-menu";
import { handleTabListKeyDown } from "../../lib/terminal-tabs";
import { useChatSelectionPosition } from "../../hooks/useChatSelectionPosition";
import { annotationBody, annotationTextRange, highlightChatAnnotation } from "../../lib/chat-annotation-navigation";
import { actionMenuContentClass, actionMenuItemClass } from "../ui/menu-styles";
import { cn } from "../../lib/utils";
import { MAX_SESSION_DISPLAY_NAME_LEN } from "../../hooks/useSessionRename";

type Side = components["schemas"]["SideConversation"];
type Snapshot = components["schemas"]["SideSnapshot"];

export function SideComposer({
	draft,
	onChange,
	onSend,
	onInterrupt,
	onAttach,
	files,
	onRemoveFile,
	ready,
	running,
	sending,
	deliveryPending = false,
	models,
	skills,
	side,
	onSettings,
	onRemoveReference,
	onFocusSide,
	onCompact,
	focusKey,
	queuedDock,
	onNavigate,
}: {
	draft: SideChatDraft;
	onChange: (text: string) => void;
	onSend: (text: string) => void;
	onInterrupt: () => void;
	onAttach: (files: File[]) => void;
	files: File[];
	onRemoveFile: (index: number) => void;
	ready: boolean;
	running: boolean;
	sending: boolean;
	deliveryPending?: boolean;
	models: ChatModel[];
	skills: ChatSkill[];
	side: Side;
	onSettings: (model: string, effort: string) => void;
	onRemoveReference: (id: string) => void;
	onFocusSide: () => void;
	onCompact: () => void;
	focusKey: number;
	queuedDock?: ReactNode;
	onNavigate?: (annotation: ChatAnnotationSummaryItem) => void;
}) {
	const { t } = useTranslation();
	const editor = useRef<ComposerEditorHandle>(null);
	const filePicker = useRef<HTMLInputElement>(null);
	const btwHandled = useRef(false);
	const compactHandled = useRef(false);
	const [highlighted, setHighlighted] = useState(0);
	const [dismissed, setDismissed] = useState<string>();
	const [trigger, setTrigger] = useState<ComposerTrigger>();
	const commands = useMemo(
		() => [
			{
				name: "btw",
				displayName: "btw",
				description: "Opens a side chat",
				source: "AO",
			},
			{
				name: "compact",
				displayName: "compact",
				description: "Compacts this side chat",
				source: "AO",
			},
			...skills.filter((skill) => skill.name !== "btw" && skill.name !== "compact"),
		],
		[skills],
	);
	const suggestions = trigger?.kind === "skill" && trigger.key !== dismissed ? rankSkills(commands, trigger.query) : [];
	const choose = (value: string) => {
		if (value === "btw") {
			onChange((editor.current?.getSnapshot().text ?? draft.text).replace(/\/[^\s]*$/, "").trim());
			onFocusSide();
			setDismissed(trigger?.key);
			return;
		}
		if (trigger) editor.current?.insertToken(trigger, value);
	};
	useEffect(() => {
		if (editor.current?.getSnapshot().text !== draft.text) editor.current?.setText(draft.text);
	}, [draft.text]);
	useEffect(() => {
		if (focusKey && ready) editor.current?.focus();
	}, [focusKey, ready]);
	const canSend =
		ready &&
		!sending &&
		!deliveryPending &&
		Boolean(draft.text.trim() || files.length || draft.attachments.length || draft.references.length);
	return (
		<div className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
			<div aria-hidden="true" className="chat-composer-fade" />
			<div className="mx-auto w-full max-w-3xl">
				{queuedDock}
				<form
					className="cursor-chat-composer relative mx-auto flex w-full max-w-3xl cursor-text flex-col gap-1.5 px-3 pt-3 pb-3"
					onSubmit={(event) => {
						event.preventDefault();
						const text = editor.current?.getSnapshot().text ?? draft.text;
						if (text.trim() === "/compact") {
							onCompact();
							return;
						}
						if (canSend) onSend(text);
					}}
					onClick={(event) => {
						if (event.target === event.currentTarget) editor.current?.focus();
					}}
				>
					{suggestions.length && trigger ? (
						<ComposerSuggestMenu
							id="side-chat-completions"
							kind="skill"
							items={suggestions}
							highlighted={Math.min(highlighted, suggestions.length - 1)}
							onPick={choose}
						/>
					) : null}
					<ChatAnnotationSummary
						annotations={draft.references}
						onSelect={onNavigate}
						onRemove={(ref) => ref.id && onRemoveReference(ref.id)}
					/>
					{draft.attachments.length ? (
						<ul className="flex flex-wrap gap-1.5" aria-label={t("sideChat.stagedFiles")}>
							{draft.attachments.map((file, index) => (
								<li
									key={file.id}
									className="flex items-center gap-1.5 rounded border border-border px-2 py-1 text-[11px]"
								>
									{file.name}
									<button
										type="button"
										aria-label={t("sideChat.remove", { value1: file.name })}
										onClick={() => onRemoveFile(index)}
									>
										<X className="size-3" />
									</button>
								</li>
							))}
						</ul>
					) : null}
					{files.length ? (
						<ul className="flex flex-wrap gap-1.5" aria-label={t("sideChat.attachedFiles")}>
							{files.map((file, index) => (
								<li
									key={`${file.name}-${index}`}
									className="flex items-center gap-1.5 rounded border border-border bg-background px-2 py-1 text-[11px]"
								>
									<span className="max-w-[120px] truncate" title={file.name}>
										{file.name}
									</span>
									<button
										type="button"
										aria-label={t("sideChat.remove", { value1: file.name })}
										onClick={() => onRemoveFile(index + draft.attachments.length)}
									>
										<X className="size-3" />
									</button>
								</li>
							))}
						</ul>
					) : null}
					<ComposerEditor
						ref={editor}
						label="Side chat question"
						placeholder={
							ready
								? running
									? t("sideChat.agentIsWorkingThisSendsWhenItFinishes")
									: t("sideChat.messageTheAgent")
								: t("sideChat.theControllerIsNotConnected")
						}
						menuOpen={suggestions.length > 0}
						menuId="side-chat-completions"
						activeIndex={Math.min(highlighted, suggestions.length - 1)}
						onChange={(next) => {
							onChange(next.text);
							setTrigger(next.trigger);
							setDismissed(undefined);
							setHighlighted(0);
						}}
						onComplete={(next, key) => {
							const matches = next.trigger?.kind === "skill" ? rankSkills(commands, next.trigger.query) : [];
							const chosen = matches[Math.min(highlighted, matches.length - 1)];
							if (chosen?.value === "btw" && key === "Enter") {
								btwHandled.current = true;
								onChange(next.text.replace(/\/[^\s]*$/, "").trim());
								onFocusSide();
								setDismissed(next.trigger?.key);
								return undefined;
							}
							if (chosen?.value === "compact" && key === "Enter" && next.text.trim() === "/compact") {
								compactHandled.current = true;
								onCompact();
								return undefined;
							}
							return chosen?.value;
						}}
						onEnter={(next, event) => {
							if (event.shiftKey) return false;
							if (btwHandled.current) {
								btwHandled.current = false;
								return true;
							}
							if (compactHandled.current) {
								compactHandled.current = false;
								return true;
							}
							if (/^\/btw$/i.test(next.text.trim())) {
								onChange("");
								onFocusSide();
								return true;
							}
							if (next.text.trim() === "/compact") {
								onCompact();
								return true;
							}
							if (
								ready &&
								!sending &&
								!deliveryPending &&
								(next.text.trim() || files.length || draft.attachments.length || draft.references.length)
							)
								onSend(next.text);
							return true;
						}}
						onCompositionChange={() => {}}
						onKeyDown={(event) => {
							if (event.key === "ArrowDown" || event.key === "ArrowUp") {
								event.preventDefault();
								setHighlighted((value) => moveHighlight(value, event.key === "ArrowDown" ? 1 : -1, suggestions.length));
							}
						}}
						onPaste={() => {}}
					/>
					<div className="flex h-7 items-center gap-1.5">
						<div
							role="group"
							aria-label={t("sideChat.messageTools")}
							className="flex min-w-0 flex-1 items-center gap-0.5"
						>
							<input
								ref={filePicker}
								type="file"
								multiple
								hidden
								onChange={(event) => {
									onAttach(Array.from(event.target.files ?? []));
									event.target.value = "";
								}}
							/>
							<Button
								type="button"
								variant="ghost"
								size="icon-sm"
								disabled={!ready || sending}
								aria-label={t("sideChat.attachAFile")}
								onClick={() => filePicker.current?.click()}
								className="size-7 shrink-0 rounded-full p-0 text-muted-foreground hover:bg-white/5! hover:text-foreground"
							>
								<Plus aria-hidden="true" className="size-3.5" />
							</Button>
							<TurnSettingsBar
								models={models}
								settings={{ model: side.model, reasoningEffort: side.effort }}
								onChange={(next) => onSettings(next.model ?? "", next.reasoningEffort ?? "")}
								showApprovalMode={false}
								disabled={!ready || sending}
							/>
						</div>
						<Button
							type={running && !canSend ? "button" : "submit"}
							variant="ghost"
							size="icon-sm"
							aria-label={running && !canSend ? t("sideChat.stopTurn") : t("sideChat.sendMessage")}
							onClick={running && !canSend ? onInterrupt : undefined}
							disabled={running && !canSend ? false : !canSend}
							className="size-7 rounded-full bg-foreground text-background hover:bg-foreground/90 hover:text-background"
						>
							{running && !canSend ? (
								<Square aria-hidden="true" className="size-2.5 fill-current" />
							) : sending ? (
								<Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
							) : (
								<ArrowUp aria-hidden="true" className="size-3.5" />
							)}
						</Button>
					</div>
				</form>
			</div>
		</div>
	);
}

async function sideFilePayload(file: File): Promise<{ mimeType: string; data: string }> {
	const dataUrl = await new Promise<string>((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result));
		reader.onerror = () => reject(reader.error);
		reader.readAsDataURL(file);
	});
	return {
		mimeType: file.type || "application/octet-stream",
		data: dataUrl.split(",", 2)[1] ?? "",
	};
}

export function useIndependentSideChats(
	sessionId: string,
	models: ChatModel[],
	skills: ChatSkill[],
	stageAttachments: (items: { mimeType: string; data: string }[]) => Promise<string[]>,
	nativeImages: boolean,
	enabled = true,
	onNavigate?: (annotation: ChatAnnotationSummaryItem) => void,
	_onHide?: () => void,
) {
	const { t } = useTranslation();
	const [sides, setSides] = useState<Side[]>([]);
	const [activeId, setActiveId] = useState<string>();
	const [visible, setVisible] = useState(false);
	const [snapshot, setSnapshot] = useState<Snapshot>();
	const [snapshotRefreshKey, setSnapshotRefreshKey] = useState(0);
	const [olderPages, setOlderPages] = useState<Snapshot[]>([]);
	const [drafts, setDrafts] = useState<Record<string, SideChatDraft>>({});
	const draftsRef = useRef(drafts);
	draftsRef.current = drafts;
	const [attachments, setAttachments] = useState<Record<string, { path: string; file: File }[]>>({});
	const [focusKey, setFocusKey] = useState(0);
	const [renameText, setRenameText] = useState<string>();
	const [renameId, setRenameId] = useState<string>();
	const renameCancelled = useRef(false);
	const [confirmClose, setConfirmClose] = useState<string>();
	const [closing, setClosing] = useState(false);
	const [closeError, setCloseError] = useState<string>();
	const [selectionAction, setSelectionAction] = useState<{ excerpt: ChatDraftExcerptReference; range: Range }>();
	const selectionButton = useRef<HTMLDivElement>(null);
	const clearSelection = useCallback(() => setSelectionAction(undefined), []);

	const [navigationTarget, setNavigationTarget] = useState<ChatAnnotationSummaryItem>();
	const highlightCleanup = useRef<(() => void) | undefined>(undefined);
	const highlightTimer = useRef<number | undefined>(undefined);
	const [error, setError] = useState<string>();
	const [pending, setPending] = useState(false);
	const [, setSendingRevision] = useState(0);
	const sendingRef = useRef(new Set<string>());
	const sending = Boolean(activeId && sendingRef.current.has(activeId));
	const [editingTurnId, setEditingTurnId] = useState<string>();
	const [editingText, setEditingText] = useState("");
	const [inputAnswers, setInputAnswers] = useState<Record<string, Record<string, string>>>({});
	const activeIdRef = useRef(activeId);
	activeIdRef.current = activeId;
	const closedSidesRef = useRef(new Set<string>());
	const loadingOlderRef = useRef(false);
	const timelineRef = useRef<HTMLDivElement>(null);
	const timelineContentRef = useRef<HTMLDivElement>(null);
	const selectionPosition = useChatSelectionPosition(
		selectionAction?.range,
		timelineRef,
		selectionButton,
		clearSelection,
	);
	const followOutputRef = useRef(true);
	const annotationNavigationHold = useRef(false);
	useEffect(() => {
		followOutputRef.current = true;
		annotationNavigationHold.current = false;
	}, [activeId]);
	useEffect(() => {
		const viewport = timelineRef.current;
		const content = timelineContentRef.current;
		if (!viewport || !content) return;
		const follow = () => {
			if (followOutputRef.current) viewport.scrollTop = viewport.scrollHeight;
		};
		follow();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(follow);
		observer.observe(content);
		return () => observer.disconnect();
	}, [activeId, visible, snapshot?.side.id]);
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const sessionRef = useRef(sessionId);
	sessionRef.current = enabled ? sessionId : "";
	useEffect(() => {
		closedSidesRef.current.clear();
		setSides([]);
		setActiveId(undefined);
		setVisible(false);
		setSnapshot(undefined);
		setOlderPages([]);
		setDrafts({});
		setAttachments({});
		setRenameId(undefined);
		setRenameText(undefined);
		setConfirmClose(undefined);
		setCloseError(undefined);
		setError(undefined);
	}, [sessionId, enabled]);

	const refreshList = useCallback(async () => {
		if (!enabled) return;
		const { data, error: requestError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats", {
			params: { path: { sessionId } },
		});
		if (sessionRef.current !== sessionId) return;
		if (requestError) {
			setError(apiErrorMessage(requestError));
			return;
		}
		const nextSides = Array.isArray(data?.sides) ? data.sides : [];
		setSides(nextSides);
		setActiveId((current) =>
			current && nextSides.some((side) => side.id === current) ? current : nextSides.at(-1)?.id,
		);
	}, [sessionId, enabled]);

	useEffect(() => {
		if (!enabled) return;
		const refresh = () => void refreshList().catch((error) => setError(String(error)));
		refresh();
		const timer = window.setInterval(refresh, 2000);
		return () => window.clearInterval(timer);
	}, [refreshList, enabled]);

	useEffect(() => {
		setOlderPages([]);
		loadingOlderRef.current = false;
		if (!enabled || !activeId) {
			setSnapshot(undefined);
			return;
		}
		let alive = true;
		let lastApplied = 0;
		let issued = 0;
		const refresh = async () => {
			const requestNumber = ++issued;
			const { data, error: requestError } = await apiClient.GET(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}",
				{
					params: { path: { sessionId, sideId: activeId } },
				},
			);
			if (!alive || requestNumber < lastApplied) return;
			lastApplied = requestNumber;
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			if (data?.snapshot) {
				const next = data.snapshot;
				setSnapshot((current) => {
					if (!current || current.side.id !== next.side.id) return next;
					// The latest page moves forward as turns arrive. Retain its
					// previously loaded tail so it cannot leave a gap above older pages.
					return {
						...next,
						hasMore: current.hasMore,
						turns: [
							...new Map([...(current.turns ?? []), ...(next.turns ?? [])].map((turn) => [turn.id, turn])).values(),
						].sort((a, b) => a.createdAt.localeCompare(b.createdAt)),
						messages: [
							...new Map([...(current.messages ?? []), ...(next.messages ?? [])].map((message) => [message.id, message])).values(),
						],
						activities: [
							...new Map([...(current.activities ?? []), ...(next.activities ?? [])].map((activity) => [activity.id, activity])).values(),
						],
					};
				});
				setError(undefined);
			}
		};
		const refreshSafely = () =>
			void refresh().catch((error) => {
				if (alive) setError(String(error));
			});
		refreshSafely();
		let stream: EventSource | undefined;
		if (baseUrl && typeof EventSource !== "undefined") {
			stream = new EventSource(
				`${baseUrl}/api/v1/sessions/${encodeURIComponent(sessionId)}/conversation/side-chats/${encodeURIComponent(activeId)}/events`,
			);
			stream.addEventListener("changed", refreshSafely);
			stream.addEventListener("snapshot", refreshSafely);
		}
		const timer = window.setInterval(refreshSafely, stream ? 3000 : 500);
		return () => {
			alive = false;
			window.clearInterval(timer);
			stream?.close();
		};
	}, [sessionId, activeId, baseUrl, snapshotRefreshKey, enabled]);

	const loadOlder = useCallback(async () => {
		if (!activeId || !snapshot || loadingOlderRef.current) return;
		const oldest = olderPages.at(-1)?.turns?.[0]?.createdAt ?? snapshot.turns?.[0]?.createdAt;
		if (!oldest) return;
		loadingOlderRef.current = true;
		try {
			const { data, error: requestError } = await apiClient.GET(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}",
				{
					params: {
						path: { sessionId, sideId: activeId },
						query: { before: oldest, limit: 50 },
					},
				},
			);
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			if (data?.snapshot) setOlderPages((current) => [...current, data.snapshot]);
		} finally {
			loadingOlderRef.current = false;
		}
	}, [sessionId, activeId, snapshot, olderPages]);

	useEffect(() => {
		if (!activeId || drafts[activeId] !== undefined) return;
		void apiClient
			.GET("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
				params: { path: { sessionId, sideId: activeId } },
			})
			.then(({ data }) => {
				if (sessionRef.current !== sessionId) return;
				if (data)
					setDrafts((current) => ({
						...current,
						[activeId]: current[activeId] ?? parseSideChatDraft(data.contentJson),
					}));
			})
			.catch((error) => {
				if (sessionRef.current === sessionId) setError(String(error));
			});
	}, [sessionId, activeId, drafts]);

	const create = useCallback(
		async (excerpt?: ChatDraftExcerptReference, forceNew = false) => {
			if (!enabled) throw new Error("Side chats are currently available only for local AO sessions.");
			setPending(true);
			setError(undefined);
			try {
				const { data, error: requestError } = await apiClient.POST(
					"/api/v1/sessions/{sessionId}/conversation/side-chats",
					{
						params: { path: { sessionId } },
						body: {
							idempotencyKey: crypto.randomUUID(),
							forceNew,

							reference: excerpt
								? {
										conversationId: excerpt.conversationId,
										messageId: excerpt.messageId,
										revision: excerpt.revision,
										text: excerpt.text,
									}
								: undefined,
						},
					},
				);
				if (requestError) throw requestError;
				if (sessionRef.current !== sessionId || closedSidesRef.current.has(data.side.id))
					throw new Error("Side chat is no longer active.");
				await aoBridge.sideChats.capture().catch(() => undefined);
				setSides((current) =>
					current.some((side) => side.id === data.side.id)
						? current.map((side) => (side.id === data.side.id ? data.side : side))
						: [...current, data.side],
				);
				setActiveId(data.side.id);
				setVisible(true);
				setFocusKey((key) => key + 1);
				if (excerpt) addReference(data.side.id, excerpt);
				return { id: data.side.id };
			} catch (cause) {
				setError(apiErrorMessage(cause));
				throw cause;
			} finally {
				setPending(false);
			}
		},
		[sessionId, enabled],
	);

	const updateDraft = useCallback((sideId: string, text: string) => {
		setDrafts((current) => ({
			...current,
			[sideId]: { ...(current[sideId] ?? emptySideChatDraft()), text },
		}));
	}, []);
	const replaceDraft = useCallback(
		async (sideId: string, draft: SideChatDraft) => {
			draft = {
				...draft,
				pendingDelivery: draftsRef.current[sideId]?.pendingDelivery,
			};
			const { error: requestError } = await apiClient.PUT(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft",
				{
					params: { path: { sessionId, sideId } },
					body: { contentJson: JSON.stringify(draft) },
				},
			);
			if (requestError) throw requestError;
			await aoBridge.sideChats.capture();
			if (sessionRef.current !== sessionId || closedSidesRef.current.has(sideId)) return;
			draftsRef.current = { ...draftsRef.current, [sideId]: draft };
			setDrafts((current) => ({
				...current,
				[sideId]: {
					...draft,
					pendingDelivery: current[sideId]?.pendingDelivery,
				},
			}));
			setAttachments((current) => ({ ...current, [sideId]: [] }));
			setFocusKey((key) => key + 1);
		},
		[sessionId],
	);
	const addReference = useCallback((sideId: string, excerpt: ChatDraftExcerptReference) => {
		setDrafts((current) => {
			const draft = current[sideId] ?? emptySideChatDraft();
			const duplicate = draft.references.some(
				(ref) =>
					ref.conversationId === excerpt.conversationId &&
					ref.messageId === excerpt.messageId &&
					ref.revision === excerpt.revision &&
					ref.text === excerpt.text,
			);
			if (duplicate) return current;
			if (draft.references.length >= 8) {
				queueMicrotask(() =>
					setError("A side question can include up to eight references. Remove one to add another."),
				);
				return current;
			}
			return {
				...current,
				[sideId]: { ...draft, references: [...draft.references, excerpt] },
			};
		});
		setFocusKey((key) => key + 1);
	}, []);
	const attachFiles = useCallback(
		async (sideId: string, files: File[]) => {
			if (files.length === 0) return;
			try {
				const payloads = await Promise.all(files.map(sideFilePayload));
				const paths = await stageAttachments(payloads);
				if (paths.length !== files.length) throw new Error("Could not stage every attached file.");
				setDrafts((current) => ({
					...current,
					[sideId]: {
						...(current[sideId] ?? emptySideChatDraft()),
						attachments: [
							...(current[sideId]?.attachments ?? []),
							...files.map((file, index) => ({
								id: crypto.randomUUID(),
								path: paths[index],
								name: file.name,
								mimeType: file.type || "application/octet-stream",
								bytes: file.size,
							})),
						],
					},
				}));
				setAttachments((current) => ({
					...current,
					[sideId]: [...(current[sideId] ?? []), ...files.map((file, index) => ({ file, path: paths[index] }))],
				}));
			} catch (cause) {
				setError(apiErrorMessage(cause));
			}
		},
		[stageAttachments],
	);

	useEffect(() => {
		if (!activeId || drafts[activeId] === undefined) return;
		const draft = drafts[activeId];
		const save = () =>
			void apiClient
				.PUT("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
					params: { path: { sessionId, sideId: activeId } },
					body: { contentJson: JSON.stringify(draft) },
				})
				.then(({ error: requestError }) => {
					if (requestError) {
						setError(apiErrorMessage(requestError));
						return;
					}
					return aoBridge.sideChats
						.capture()
						.catch(() => setError("Could not save the side draft for daemon recovery."));
				})
				.catch((error) => {
					if (sessionRef.current === sessionId) setError(apiErrorMessage(error));
				});
		const timer = window.setTimeout(() => {
			save();
		}, 350);
		return () => {
			window.clearTimeout(timer);
			if (
				!closedSidesRef.current.has(activeId) &&
				(activeIdRef.current !== activeId || sessionRef.current !== sessionId)
			)
				save();
		};
	}, [sessionId, activeId, drafts]);

	const send = useCallback(
		async (
			sideId: string,
			text: string,
			extraAttachments: { mimeType: string; data: string }[] = [],
			overrideReferences?: ChatDraftExcerptReference[],
			propagateError = false,
		) => {
			const btw = /^\/btw(?:\s+|$)/i.exec(text);
			if (btw) {
				text = text.slice(btw[0].length).trim();
				if (!text) {
					updateDraft(sideId, text);
					setFocusKey((key) => key + 1);
					return;
				}
			}
			if (sendingRef.current.has(sideId)) {
				if (propagateError) throw new Error("Side chat delivery is still in progress. Try again after it finishes.");
				return;
			}
			sendingRef.current.add(sideId);
			setSendingRevision((version) => version + 1);
			setError(undefined);
			let receipt = draftsRef.current[sideId]?.pendingDelivery;
			try {
				const draft = draftsRef.current[sideId] ?? emptySideChatDraft();
				const files = attachments[sideId] ?? [];
				if (!receipt) {
					const imagePayloads = [
						...extraAttachments,
						...(await Promise.all(
							files
								.filter(({ file }) => /^image\/(png|jpeg|gif|webp)$/i.test(file.type))
								.map(({ file }) => sideFilePayload(file)),
						)),
					];
					const paths = extraAttachments.length ? await stageAttachments(extraAttachments) : [];
					const allPaths = [...draft.attachments.map((file) => file.path), ...paths];

					const clientMessageId = crypto.randomUUID();
					receipt = {
						clientMessageId,
						submitted: {
							text: draft.text,
							attachments: draft.attachments,
							references: draft.references,
						},
						request: {
							text,
							resources: allPaths.map((path) => ({
								uri: path,
								name: path.split(/[\\/]/).at(-1) || "Attachment",
							})),
							clientMessageId,
							attachments: nativeImages ? imagePayloads : [],
							references: (overrideReferences ?? draft.references).map((ref) => ({
								conversationId: ref.conversationId,
								messageId: ref.messageId,
								revision: ref.revision,
								text: ref.text,
							})),
						},
					};
				}
				const frozen = receipt;
				const pending = {
					...(draftsRef.current[sideId] ?? draft),
					pendingDelivery: frozen,
				};
				draftsRef.current = { ...draftsRef.current, [sideId]: pending };
				setDrafts((current) => ({
					...current,
					[sideId]: { ...(current[sideId] ?? draft), pendingDelivery: frozen },
				}));
				// Persist the receipt before dispatch so reconnect can retry the exact request.
				const saved = await apiClient.PUT("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
					params: { path: { sessionId, sideId } },
					body: { contentJson: JSON.stringify(pending) },
				});
				if (saved.error) throw saved.error;
				await aoBridge.sideChats.capture();
				if (sessionRef.current !== sessionId || closedSidesRef.current.has(sideId)) return;
				const { error: requestError, response } = await apiClient.POST(
					"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/messages",
					{
						params: { path: { sessionId, sideId } },
						body: frozen.request,
					},
				);
				if (requestError) {
					// Server errors can occur after acceptance; keep their receipt too.
					if (response && response.status < 500)
						setDrafts((current) => {
							const d = current[sideId];
							if (!d || d.pendingDelivery !== frozen) return current;
							const { pendingDelivery: _, ...rest } = d;
							return { ...current, [sideId]: rest };
						});
					throw requestError;
				}
				await aoBridge.sideChats.capture().catch(() => undefined);
				if (sessionRef.current !== sessionId || closedSidesRef.current.has(sideId)) return;
				setDrafts((current) => {
					const d = current[sideId];
					if (!d || d.pendingDelivery !== frozen) return current;
					const { pendingDelivery: _, ...rest } = d;
					const unchanged =
						d.text === frozen.submitted.text &&
						JSON.stringify(d.attachments) === JSON.stringify(frozen.submitted.attachments) &&
						JSON.stringify(d.references) === JSON.stringify(frozen.submitted.references);
					return {
						...current,
						[sideId]: unchanged ? emptySideChatDraft() : rest,
					};
				});
				setAttachments((current) => (current[sideId] === files ? { ...current, [sideId]: [] } : current));
			} catch (cause) {
				if (sessionRef.current === sessionId && !closedSidesRef.current.has(sideId)) setError(apiErrorMessage(cause));
				if (propagateError) throw cause;
			} finally {
				sendingRef.current.delete(sideId);
				setSendingRevision((version) => version + 1);
			}
		},
		[sessionId, updateDraft, attachments, stageAttachments, nativeImages],
	);

	const close = useCallback(
		async (sideId: string) => {
			// Record close intent in Electron RAM before the request can race a daemon restart.
			try {
				await aoBridge.sideChats.capture(sideId);
			} catch (cause) {
				await aoBridge.sideChats.capture(sideId, true).catch(() => undefined);
				throw cause;
			}
			const { error: requestError } = await apiClient
				.DELETE("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
					params: { path: { sessionId, sideId } },
				})
				.catch(async (cause) => {
					await aoBridge.sideChats.capture(sideId, true).catch(() => undefined);
					throw cause;
				});
			if (requestError) {
				await aoBridge.sideChats.capture(sideId, true).catch(() => undefined);
				throw new Error(apiErrorMessage(requestError));
			}
			if (sessionRef.current !== sessionId) return;
			closedSidesRef.current.add(sideId);
			await aoBridge.sideChats.capture().catch(() => undefined);
			if (sessionRef.current !== sessionId) return;
			setDrafts((current) => {
				const next = { ...current };
				delete next[sideId];
				return next;
			});
			setAttachments((current) => {
				const next = { ...current };
				delete next[sideId];
				return next;
			});
			const remaining = sides.filter((side) => side.id !== sideId);
			setSides(remaining);
			if (activeId === sideId) {
				setActiveId(
					remaining[
						Math.min(
							sides.findIndex((side) => side.id === sideId),
							remaining.length - 1,
						)
					]?.id,
				);
				setSnapshot(undefined);
			}
		},
		[sessionId, activeId, sides],
	);

	const interrupt = useCallback(
		async (sideId: string) => {
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/interrupt",
				{
					params: { path: { sessionId, sideId } },
				},
			);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const compact = useCallback(
		async (sideId: string) => {
			setError(undefined);
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/compact",
				{
					params: { path: { sessionId, sideId } },
				},
			);
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			updateDraft(sideId, "");
		},
		[sessionId, updateDraft],
	);
	const resolveApproval = useCallback(
		async (sideId: string, requestId: string, decisionId: string) => {
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/approvals/{requestId}/resolve",
				{
					params: { path: { sessionId, sideId, requestId } },
					body: { decisionId },
				},
			);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const resolveInput = useCallback(
		async (sideId: string, requestId: string, action: "accept" | "decline" | "cancel") => {
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/inputs/{requestId}/resolve",
				{
					params: { path: { sessionId, sideId, requestId } },
					body: {
						action,
						content: action === "accept" ? (inputAnswers[requestId] ?? {}) : {},
					},
				},
			);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId, inputAnswers],
	);
	const updateSettings = useCallback(
		async (sideId: string, model: string, effort: string) => {
			const { error: requestError } = await apiClient.PATCH(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/settings",
				{
					params: { path: { sessionId, sideId } },
					body: { model, effort },
				},
			);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const retry = useCallback(
		async (sideId: string, turnId: string) => {
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/retry",
				{
					params: { path: { sessionId, sideId, turnId } },
				},
			);
			if (requestError) setError(apiErrorMessage(requestError));
		},
		[sessionId],
	);
	const saveQueuedEdit = useCallback(
		async (sideId: string) => {
			if (!editingTurnId || !editingText.trim()) return;
			const { error: requestError } = await apiClient.PATCH(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}",
				{
					params: { path: { sessionId, sideId, turnId: editingTurnId } },
					body: { text: editingText, clientMessageId: "" },
				},
			);
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			setEditingTurnId(undefined);
		},
		[sessionId, editingTurnId, editingText],
	);
	const timeline = snapshot
		? [
				...[
					...olderPages
						.slice()
						.reverse()
						.flatMap((page) => page.messages ?? []),
					...(snapshot.messages ?? []),
				].map((message) => ({
					kind: "message" as const,
					time: message.createdAt,
					sequence: message.sequence,
					message,
				})),
				...[
					...olderPages
						.slice()
						.reverse()
						.flatMap((page) => page.activities ?? []),
					...(snapshot.activities ?? []),
				].map((activity) => ({
					kind: "activity" as const,
					time: activity.createdAt,
					sequence: 0,
					activity,
				})),
			].sort((a, b) => a.time.localeCompare(b.time) || a.sequence - b.sequence)
		: [];

	const currentActiveId = sides.some((side) => side.id === activeId && side.sessionId === sessionId)
		? activeId
		: undefined;
	const activeNumber = sides.findIndex((side) => side.id === currentActiveId) + 1;
	const captureSelection = useCallback(() => {
		const selection = window.getSelection();
		const anchor =
			selection?.anchorNode instanceof Element ? selection.anchorNode : selection?.anchorNode?.parentElement;
		const focus = selection?.focusNode instanceof Element ? selection.focusNode : selection?.focusNode?.parentElement;
		const source = anchor?.closest<HTMLElement>("[data-chat-message-id]");
		const message = [...(snapshot?.messages ?? []), ...olderPages.flatMap((page) => page.messages ?? [])].find(
			(message) => message.id === source?.dataset.chatMessageId,
		);
		const turn = [...(snapshot?.turns ?? []), ...olderPages.flatMap((page) => page.turns ?? [])].find(
			(turn) => turn.id === message?.turnId,
		);
		if (
			!selection ||
			selection.isCollapsed ||
			!selection.rangeCount ||
			!source ||
			!timelineContentRef.current?.contains(source) ||
			source !== focus?.closest("[data-chat-message-id]") ||
			!message ||
			message.streaming ||
			turn?.state !== "completed" ||
			!activeId ||
			!selection.toString().trim()
		) {
			clearSelection();
			return;
		}
		setSelectionAction({
			excerpt: {
				id: crypto.randomUUID(),
				conversationId: activeId,
				messageId: message.id,
				revision: message.revision,
				text: selection.toString().trim(),
				role: message.role as "user" | "assistant",
			},
			range: selection.getRangeAt(0).cloneRange(),
		});
	}, [activeId, snapshot, olderPages, clearSelection]);
	const navigateAnnotation = useCallback(
		(annotation: ChatAnnotationSummaryItem) => {
			if (annotation.conversationId === activeId) {
				navigationPageRef.current = undefined;
				annotationNavigationHold.current = true;
				followOutputRef.current = false;
				setNavigationTarget(annotation);
			} else onNavigate?.(annotation);
		},
		[activeId, onNavigate],
	);
	useEffect(() => {
		clearSelection();
		setNavigationTarget(undefined);
		highlightCleanup.current?.();
		window.clearTimeout(highlightTimer.current);
	}, [activeId, clearSelection]);
	useEffect(
		() => () => {
			highlightCleanup.current?.();
			window.clearTimeout(highlightTimer.current);
		},
		[],
	);
	const navigationPageRef = useRef<string | undefined>(undefined);
	useEffect(() => {
		if (!navigationTarget || !activeId || navigationTarget.conversationId !== activeId) return;
		const source = Array.from(
			timelineContentRef.current?.querySelectorAll<HTMLElement>("[data-chat-message-id]") ?? [],
		).find((node) => node.dataset.chatMessageId === navigationTarget.messageId);
		if (!source) {
			if (loadingOlderRef.current) return;
			const oldest = olderPages.at(-1)?.turns?.[0]?.createdAt ?? snapshot?.turns?.[0]?.createdAt;
			if ((olderPages.at(-1)?.hasMore ?? snapshot?.hasMore) && oldest && navigationPageRef.current !== oldest) {
				navigationPageRef.current = oldest;
				void loadOlder();
				return;
			}
			setError("This annotation's source is unavailable. Reselect the text to create a new reference.");
			setNavigationTarget(undefined);
			return;
		}
		navigationPageRef.current = undefined;
		followOutputRef.current = false;
		highlightCleanup.current?.();
		window.clearTimeout(highlightTimer.current);
		highlightCleanup.current = highlightChatAnnotation(source, navigationTarget.text, navigationTarget.revision);
		const viewport = timelineRef.current;
		const range = annotationTextRange(annotationBody(source), navigationTarget.text);
		const rect = range?.getBoundingClientRect() ?? annotationBody(source).getBoundingClientRect();
		const bounds = viewport?.getBoundingClientRect();
		if (viewport && bounds && (rect.top < bounds.top + 12 || rect.bottom > bounds.bottom - 12)) {
			viewport.scrollTo({
				top: viewport.scrollTop + rect.top + rect.height / 2 - (bounds.top + bounds.height / 2),
				behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
			});
		}
		highlightTimer.current = window.setTimeout(() => highlightCleanup.current?.(), 2200);
		setNavigationTarget(undefined);
	}, [navigationTarget, activeId, snapshot, olderPages, loadOlder]);
	const tabStripRef = useRef<HTMLDivElement>(null);
	useEffect(() => {
		const strip = tabStripRef.current;
		const selected = strip?.querySelector<HTMLElement>('[aria-selected="true"]');
		if (!strip || !selected) return;
		const bounds = strip.getBoundingClientRect(),
			rect = selected.getBoundingClientRect();
		if (rect.left < bounds.left) strip.scrollLeft += rect.left - bounds.left;
		else if (rect.right > bounds.right) strip.scrollLeft += rect.right - bounds.right;
	}, [currentActiveId, sides.length]);
	const beginRename = (side: Side) => {
		renameCancelled.current = false;
		setRenameId(side.id);
		setRenameText(side.label);
	};
	const saveRename = async () => {
		if (renameCancelled.current || !renameId) return;
		const id = renameId, label = renameText?.trim();
		setRenameId(undefined);
		setRenameText(undefined);
		if (!label || label === sides.find((side) => side.id === id)?.label) return;
		const { error: requestError } = await apiClient.PATCH(
			"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/label",
			{ params: { path: { sessionId, sideId: id } }, body: { label } },
		);
		if (requestError) {
			setError(apiErrorMessage(requestError));
			return;
		}
		await refreshList();
	};

	const tabBar = enabled ? (
		<header className="flex shrink-0 min-w-0 items-center border-b border-border bg-background">
			<div
				ref={tabStripRef}
				role="tablist"
				aria-label={t("sideChat.tabs")}
				onKeyDown={handleTabListKeyDown}
				className="flex min-w-0 flex-1 overflow-x-auto"
			>
				{sides.map((side) => {
					const tab = (
						<div key={side.id} className={cn("browser-panel__tab", side.id === currentActiveId && "browser-panel__tab--active")}>
								{renameId === side.id ? (
									<input
										autoFocus
										aria-label={t("sideChat.renameSideChat")}
										className="min-w-0 w-36 flex-1 rounded-xs border border-accent bg-background px-1 text-control text-foreground outline-none ring-1 ring-accent"
										maxLength={MAX_SESSION_DISPLAY_NAME_LEN}
										onFocus={(event) => event.currentTarget.select()}
										onBlur={() => void saveRename()}
										value={renameText ?? ""}
										onChange={(event) => setRenameText(event.target.value)}
										onKeyDown={(event) => {
											if (event.key === "Enter") {
												event.preventDefault();
												event.currentTarget.blur();
											}
											if (event.key === "Escape") {
												event.preventDefault();
												renameCancelled.current = true;
												setRenameId(undefined);
												setRenameText(undefined);
											}
										}}
									/>
								) : (
									<button
										type="button"
										role="tab"
										aria-selected={side.id === currentActiveId}
										tabIndex={side.id === currentActiveId ? 0 : -1}
										className="browser-panel__tab-select"
										onClick={(event) => {
											if (event.detail > 1) return;
											setActiveId(side.id);
											setFocusKey(0);
										}}
										aria-keyshortcuts="F2"
										onDoubleClick={(event) => { event.preventDefault(); beginRename(side); }}
										onKeyDown={(event) => {
											if (event.key === "F2") {
												event.preventDefault();
												beginRename(side);
											}
										}}
										title={side.label}
									>
										<MessageSquare className="browser-panel__tab-icon" aria-hidden="true" />
										<span className="browser-panel__tab-title">
											{side.label?.trim() || "Side Chat"}
										</span>
									</button>
								)}
								<button
									type="button"
									hidden={renameId === side.id}
									className="browser-panel__tab-close"
									aria-label={t("sideChat.closeNamed", { name: side.label })}
									onClick={() => {
										setCloseError(undefined);
										setConfirmClose(side.id);
									}}
								>
									<X className="size-3.5" aria-hidden="true" />
								</button>
							</div>
					);
					if (renameId === side.id) return tab;
					return (
						<ContextMenu key={side.id}>
							<ContextMenuTrigger asChild>{tab}</ContextMenuTrigger>
							<ContextMenuContent className="min-w-44">
								<ContextMenuItem onSelect={() => beginRename(side)}>
									<Pencil aria-hidden="true" />
									{t("sideChat.rename")}
								</ContextMenuItem>
							</ContextMenuContent>
						</ContextMenu>
					);
				})}
			</div>
			<Button
				type="button"
				variant="ghost"
				size="icon-sm"
				className="shrink-0"
				disabled={pending}
				aria-label={t("sideChat.newSideChat")}
				onClick={() => void create(undefined, true).catch(() => undefined)}
			>
				<Plus className="size-3.5" />
			</Button>
		</header>
	) : null;
	const closeDialog = (
		<ConfirmDialog
			open={Boolean(confirmClose)}
			title={t("sideChat.closeNamedConfirm", { name: sides.find((side) => side.id === confirmClose)?.label ?? t("inspector.sideChat") })}
			description={t("sideChat.closeExplanation")}
			confirmLabel={t("sideChat.closeSideChat")}
			cancelLabel={t("sideChat.keepSideChat")}
			destructive
			busy={closing}
			error={closeError}
			onOpenChange={(open) => {
				if (!open && !closing) setConfirmClose(undefined);
			}}
			onConfirm={() => {
				if (!confirmClose) return;
				setClosing(true);
				void close(confirmClose)
					.then(() => setConfirmClose((current) => (current === confirmClose ? undefined : current)))
					.catch((cause) => setCloseError(apiErrorMessage(cause)))
					.finally(() => setClosing(false));
			}}
		/>
	);
	const panel =
		enabled && currentActiveId && visible ? (
			<aside
				aria-label={t("sideChat.sideChats")}
				className="cursor-chat-surface relative flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-background [font-size:14px]"
			>
				{tabBar}
				{closeDialog}
				{selectionAction ? (
					<div
						ref={selectionButton}
						style={{
							left: selectionPosition?.left ?? 0,
							top: selectionPosition?.top ?? 0,
							visibility: selectionPosition?.visible ? "visible" : "hidden",
						}}
						onMouseDown={(event) => event.preventDefault()}
						className={cn(actionMenuContentClass, "absolute z-20 flex-row min-w-0 w-max max-w-full shadow-lg")}
					>
						<button
							type="button"
							className={cn(actionMenuItemClass, "shrink-0 whitespace-nowrap hover:bg-interactive-hover")}
							onClick={() => {
								addReference(currentActiveId, selectionAction.excerpt);
								clearSelection();
								window.getSelection()?.removeAllRanges();
							}}
						>
							<MessageSquarePlus className="size-3.5" />
							{t("sideChat.addToChat")}
						</button>
					</div>
				) : null}
				{snapshot && snapshot.side.id === activeId ? (
					<>
						{snapshot.side.state !== "ready" ? (
							<p className="border-b border-border px-4 py-2 text-xs text-muted-foreground" role="status">
								{snapshot.side.errorMessage ||
									(snapshot.side.state === "recovering" ? "Reconnecting side chat…" : snapshot.side.state)}
							</p>
						) : null}
						<div className="relative min-h-0 flex-1">
							<div
								ref={timelineRef}
								onMouseUp={captureSelection}
								onKeyUp={captureSelection}
								onWheel={() => {
									annotationNavigationHold.current = false;
								}}
								onPointerDown={() => {
									annotationNavigationHold.current = false;
								}}
								onKeyDown={() => {
									annotationNavigationHold.current = false;
								}}
								onScroll={(event) => {
									if (annotationNavigationHold.current) return;
									const element = event.currentTarget;
									followOutputRef.current = element.scrollHeight - element.clientHeight - element.scrollTop < 48;
								}}
								className="cursor-chat-timeline select-text h-full min-w-0 overflow-x-hidden overflow-y-auto px-4 py-5 [overflow-wrap:anywhere]"
								role="log"
								aria-label={t("sideChat.sideChatMessages", {
									value1: activeNumber,
								})}
							>
								<div ref={timelineContentRef} className={cn("mx-auto flex w-full min-w-0 max-w-3xl flex-col gap-5", timeline.length === 0 && !(snapshot.turns ?? []).length && "min-h-full justify-center")}>
									{timeline.length === 0 && !(snapshot.turns ?? []).length ? (
										<div className="mx-auto flex max-w-sm flex-col items-center gap-2 py-8 text-center text-sm">
											<h3 className="font-medium">{t("sideChat.askASideQuestion")}</h3>
											<p className="text-xs leading-relaxed text-muted-foreground">
												{t("sideChat.isolationPolicy")}
											</p>
										</div>
									) : null}
									{(olderPages.at(-1)?.hasMore ?? snapshot.hasMore) ? (
										<button
											type="button"
											className="self-center text-xs text-muted-foreground underline"
											onClick={() => void loadOlder()}
										>
											{t("sideChat.loadEarlierMessages")}
										</button>
									) : null}
									{timeline.map((item) =>
										item.kind === "message"
											? (() => {
													const message: ConversationMessage = {
														kind: "message",
														id: item.message.id,
														turnId: item.message.turnId,
														sequence: item.message.sequence,
														revision: item.message.revision,
														role: item.message.role === "user" ? "user" : "assistant",
														origin: item.message.role === "user" ? "human" : "provider",
														text: item.message.text,
														streaming: item.message.streaming,
														createdAt: item.message.createdAt,
														content: (
															[...(snapshot.turns ?? []), ...olderPages.flatMap((page) => page.turns ?? [])].find(
																(turn) => turn.id === item.message.turnId,
															)?.references ?? []
														).map((ref) => ({
															type: "excerpt",
															text: ref.selection,
															sourceConversationId: ref.conversationId,
															sourceMessageId: ref.messageId,
															sourceRevision: ref.revision,
														})),
													};
													return message.role === "user" ? (
														<HumanMessage
															key={message.id}
															message={message}
															sessionId={sessionId}
															onSelectAnnotation={navigateAnnotation}
														/>
													) : (
														<AssistantMessage key={message.id} message={message} showCopy={!message.streaming} />
													);
												})()
											: (() => {
													const activity = item.activity;
													const conversationActivity: ConversationActivity = {
														kind: "activity",
														id: activity.id,
														turnId: activity.turnId,
														sequence: 0,
														revision: 1,
														activityKind:
															activity.kind === "input"
																? "user_input"
																: [
																			"command",
																			"file_change",
																			"plan",
																			"reasoning",
																			"approval",
																			"usage",
																			"error",
																			"system",
																			"mcp_tool",
																			"auto_review",
																			"user_input",
																	  ].includes(activity.kind)
																	? (activity.kind as ConversationActivity["activityKind"])
																	: "system",
														status: activity.status as ConversationActivity["status"],
														summary: activity.summary || activity.kind,
														detail: (activity.kind === "command" && activity.text
															? {
																	...(activity.detail &&
																	typeof activity.detail === "object" &&
																	!Array.isArray(activity.detail)
																		? activity.detail
																		: {}),
																	output: activity.text,
																}
															: activity.detail) as ConversationActivity["detail"],
														requestId: activity.requestId,
														decisions: activity.decisions?.map((decision) => ({
															id: decision.id,
															label: decision.label,
															kind: decision.kind as NonNullable<ConversationActivity["decisions"]>[number]["kind"],
														})),
														createdAt: activity.createdAt,
													};
													return (
														<div key={activity.id}>
															{activity.kind === "approval" ? (
																<ApprovalCard
																	activity={conversationActivity}
																	onDecide={(requestId, decisionId) =>
																		void resolveApproval(activeId, requestId, decisionId)
																	}
																/>
															) : activity.kind === "user_input" || activity.kind === "input" ? null : (
																<ActivityRow activity={conversationActivity} />
															)}
															{(activity.kind === "user_input" || activity.kind === "input") &&
															activity.status === "pending" &&
															activity.requestId &&
															activity.input ? (
																<div className="space-y-2 pt-2">
																	<p>{activity.input.message}</p>
																	{activity.input.url && /^https?:\/\//i.test(activity.input.url) ? (
																		<a className="underline" href={activity.input.url} target="_blank" rel="noreferrer">
																			{t("sideChat.openRequestedUrl")}
																		</a>
																	) : null}
																	{Object.keys(
																		(activity.input.schema?.properties ?? {}) as Record<string, unknown>,
																	).map((key) => (
																		<label key={key} className="block">
																			{key}
																			<input
																				className="ml-2 rounded border border-border bg-background p-1"
																				value={inputAnswers[activity.requestId!]?.[key] ?? ""}
																				onChange={(event) =>
																					setInputAnswers((current) => ({
																						...current,
																						[activity.requestId!]: {
																							...current[activity.requestId!],
																							[key]: event.target.value,
																						},
																					}))
																				}
																			/>
																		</label>
																	))}
																	<div className="flex gap-2">
																		<Button
																			type="button"
																			size="sm"
																			onClick={() => void resolveInput(activeId, activity.requestId!, "accept")}
																		>
																			{t("sideChat.submit")}
																		</Button>
																		<Button
																			type="button"
																			size="sm"
																			variant="outline"
																			onClick={() => void resolveInput(activeId, activity.requestId!, "decline")}
																		>
																			{t("sideChat.decline")}
																		</Button>
																	</div>
																</div>
															) : null}
														</div>
													);
												})(),
									)}
									{(snapshot.turns ?? []).some((turn) => turn.state === "running") ? (
										<div className="flex min-h-6 items-center gap-2 px-1 py-0.5" data-testid="side-live-turn-status">
											<Loader2 aria-hidden="true" className="size-3 shrink-0 animate-spin text-status-working" />
											<span role="status" aria-live="polite" className="text-xs font-medium text-muted-foreground">
												{t("sideChat.working")}
											</span>
										</div>
									) : null}
									{(snapshot.turns ?? [])
										.filter((turn) => turn.state === "failed")
										.map((turn) => (
											<div key={turn.id} className="rounded-lg border border-border px-3 py-2 text-xs">
												<p role="alert" className="text-destructive">
													{turn.errorMessage}
												</p>
												<Button type="button" size="sm" variant="ghost" onClick={() => void retry(activeId, turn.id)}>
													{t("sideChat.retryQuestion")}
												</Button>
											</div>
										))}
								</div>
							</div>
						</div>
						{snapshot.side.state === "failed" && snapshot.side.recreateRequired ? (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => void create(undefined, true).catch(() => undefined)}
							>
								{t("sideChat.newSideChat")}
							</Button>
						) : snapshot.side.state === "failed" ? (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() =>
									void apiClient
										.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/recover", {
											params: { path: { sessionId, sideId: activeId } },
										})
										.then(({ error: recoveryError }) => {
											if (recoveryError) setError(apiErrorMessage(recoveryError));
										})
								}
							>
								{t("sideChat.retryConnection")}
							</Button>
						) : null}
						{drafts[activeId]?.pendingDelivery && !sendingRef.current.has(activeId) ? (
							<div className="px-3 text-xs text-muted-foreground">
								<p>{t("sideChat.deliveryIsUnconfirmedRetryTheOriginalQuestionBeforeSendingAnother")}</p>
								<Button
									type="button"
									size="sm"
									variant="ghost"
									disabled={sending}
									onClick={() => void send(activeId, "")}
								>
									{t("sideChat.retryDelivery")}
								</Button>
							</div>
						) : null}
						<SideComposer
							onNavigate={navigateAnnotation}
							draft={drafts[activeId] ?? emptySideChatDraft()}
							onChange={(text) => updateDraft(activeId, text)}
							onSend={(text) => void send(activeId, text)}
							onCompact={() => void compact(activeId)}
							onInterrupt={() => void interrupt(activeId)}
							onAttach={(files) => void attachFiles(activeId, files)}
							onRemoveFile={(index) => {
								const stagedCount = (drafts[activeId]?.attachments ?? []).length;
								if (index < stagedCount) {
									const path = drafts[activeId]?.attachments[index]?.path;
									setDrafts((current) => ({
										...current,
										[activeId]: {
											...(current[activeId] ?? emptySideChatDraft()),
											attachments: (current[activeId]?.attachments ?? []).filter((_, i) => i !== index),
										},
									}));
									setAttachments((current) => ({
										...current,
										[activeId]: (current[activeId] ?? []).filter((item) => item.path !== path),
									}));
								}
							}}
							files={[]}
							ready={snapshot.side.state === "ready"}
							deliveryPending={Boolean(drafts[activeId]?.pendingDelivery)}
							skills={skills}
							focusKey={focusKey}
							onFocusSide={() => setFocusKey((key) => key + 1)}
							onRemoveReference={(id) =>
								setDrafts((current) => ({
									...current,
									[activeId]: {
										...(current[activeId] ?? emptySideChatDraft()),
										references: (current[activeId]?.references ?? []).filter((ref) => ref.id !== id),
									},
								}))
							}
							running={(snapshot.turns ?? []).some((turn) => turn.state === "running")}
							sending={sending}
							models={models}
							side={snapshot.side}
							onSettings={(model, effort) => void updateSettings(activeId, model, effort)}
							queuedDock={
								(snapshot.turns ?? []).some((turn) => turn.state === "queued") ? (
									<>
										<QueuedMessageDock
											messages={(snapshot.turns ?? [])
												.filter((turn) => turn.state === "queued")
												.map((turn, index) => ({
													turnId: turn.id,
													message: {
														kind: "message",
														id: `queued-${turn.id}`,
														turnId: turn.id,
														sequence: index + 1,
														revision: 1,
														role: "user",
														origin: "human",
														text: turn.text,
														streaming: false,
														createdAt: turn.createdAt,
													},
												}))}
											onCancelQueuedTurn={(turnId) =>
												apiClient
													.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/cancel", {
														params: {
															path: { sessionId, sideId: activeId, turnId },
														},
													})
													.then(({ error }) => {
														if (error) setError(apiErrorMessage(error));
													})
											}
											onBeginQueuedEdit={(turnId, text) => {
												setEditingTurnId(turnId);
												setEditingText(text);
											}}
										/>
										{editingTurnId ? (
											<div className="border-x border-border bg-surface px-3 py-2 text-xs">
												<p className="mb-1 text-muted-foreground">{t("sideChat.editingQueuedMessage")}</p>
												<textarea
													aria-label={t("sideChat.editQueuedSideQuestion")}
													className="w-full rounded border border-border bg-background p-2"
													value={editingText}
													onChange={(event) => setEditingText(event.target.value)}
												/>
												<div className="flex gap-2">
													<Button type="button" size="sm" onClick={() => void saveQueuedEdit(activeId)}>
														{t("sideChat.save")}
													</Button>
													<Button type="button" size="sm" variant="ghost" onClick={() => setEditingTurnId(undefined)}>
														{t("sideChat.cancel")}
													</Button>
												</div>
											</div>
										) : null}
									</>
								) : null
							}
						/>
					</>
				) : (
					<div
						className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center"
						role="status"
					>
						{error ? (
							<MessageSquare aria-hidden="true" className="size-5 text-muted-foreground" />
						) : (
							<Loader2 aria-hidden="true" className="size-5 animate-spin text-muted-foreground" />
						)}
						<p className="text-sm font-medium">
							{error ? t("sideChat.sideChatIsUnavailable") : t("sideChat.openingSideChat")}
						</p>
						<p className="max-w-xs text-xs leading-relaxed text-muted-foreground">
							{error
								? t(
										"sideChat.yourDraftStaysInThisThreadTryLoadingItAgainOrReturnToTheMainChatWhileTheConnectionRetriesAutomatically",
									)
								: t("sideChat.preparingThisSeparateThreadYouCanKeepWorkingInTheMainChat")}
						</p>
						{error ? (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => {
									setError(undefined);
									setSnapshotRefreshKey((key) => key + 1);
								}}
							>
								{t("sideChat.retryLoading")}
							</Button>
						) : null}
					</div>
				)}
				{error ? (
					<div className="shrink-0 border-t border-border bg-surface px-4 py-3 text-xs" role="alert">
						<p className="break-words text-destructive">{error}</p>
					</div>
				) : null}
			</aside>
		) : enabled && currentActiveId ? null : (
			<div className="flex h-full min-h-0 flex-col bg-background">
				{tabBar}
				{closeDialog}
				<div className="flex flex-1 flex-col items-center justify-center gap-3 p-4 text-center text-xs text-muted-foreground">
					<p>
						{enabled ? "No side chat opened yet" : t("sideChat.sideChatsAreAvailableOnlyForLocalDesktopChatSessions")}
					</p>
					{enabled ? <p className="max-w-sm leading-relaxed">{t("sideChat.isolationPolicy")}</p> : null}
					{error ? <p role="alert">{error}</p> : null}
				</div>
			</div>
		);

	return {
		create,
		panel,
		pending,
		error,
		send,
		setQuestionDraft: updateDraft,
		replaceDraft,
		addReference,
		sides,
		activeId: currentActiveId,
		visible,
		show: (sideId?: string) => {
			if (sideId) setActiveId(sideId);
			setVisible(true);
			setFocusKey((key) => key + 1);
		},
		hide: () => setVisible(false),
		close,
	};
}
