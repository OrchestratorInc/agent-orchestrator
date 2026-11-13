import { excerptSizeError, fitExcerptContext, LOCAL_CHAT_MESSAGE_BYTES } from "../../../shared/chat-excerpt";
import { upgradeExcerpt } from "../../lib/chat-excerpt-snapshot";
import { useChatDraftTranslation } from "../../lib/chat-draft-messages";
import { apiErrorException } from "../../lib/api-error-exception";
import { useTranslation } from "react-i18next";
import { flushSync } from "react-dom";
import type { PendingSideClose, SideCloseOutcome, SideCloseState } from "../../../shared/side-close";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { ArrowUp, Loader2, MessageSquare, Pencil, Plus, Square, X } from "@/components/app-icons";
import type { components } from "../../../api/schema";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import type { ChatSkill } from "../../types/conversation";
import type { ChatModel } from "../../types/conversation";
import { apiClient, apiErrorMessage, eventStreamBaseUrl, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { ApprovalCard } from "./ChatTimelineItems";
import { Timeline } from "./ChatWorkspace";
import { sideConversationActivity, sideConversationSnapshot } from "./sideConversation";
import { ComposerEditor, type ComposerEditorHandle, type ComposerTrigger } from "./ComposerEditor";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";
import { rankSkills, moveHighlight } from "./composerSuggest";
import { ChatAnnotationSummary, type ChatAnnotationSummaryItem } from "./ChatAnnotationSummary";
import { emptySideChatDraft, parseSideChatDraft, type SideChatDraft, type SideDeliveryReceipt } from "./sideChatDraft";
import { TurnSettingsBar } from "./TurnSettingsBar";
import { QueuedMessageDock } from "./QueuedMessageDock";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { ConfirmDialog } from "../ConfirmDialog";
import { ContextMenu, ContextMenuTrigger, ContextMenuContent, ContextMenuItem } from "../ui/context-menu";
import { handleTabListKeyDown } from "../../lib/terminal-tabs";
import { saveSideChatDraft, readSideChatDraft } from "./sideChatDraftWriter";
import { useTabScrollEdges } from "../../hooks/useTabScrollEdges";
import { cn } from "../../lib/utils";
import { MAX_SESSION_DISPLAY_NAME_LEN } from "../../hooks/useSessionRename";
import { useUiStore } from "../../stores/ui-store";

type Side = components["schemas"]["SideConversation"];
type Snapshot = components["schemas"]["SideSnapshot"];

function appendReference(draft: SideChatDraft, excerpt: ChatDraftExcerptReference): { draft: SideChatDraft; error?: string } {
	const duplicate = draft.references.some(ref => ref.conversationId === excerpt.conversationId &&
		ref.messageId === excerpt.messageId && ref.revision === excerpt.revision && ref.text === excerpt.text &&
		JSON.stringify(ref.snapshot?.sourceRange) === JSON.stringify(excerpt.snapshot?.sourceRange) &&
		JSON.stringify(ref.snapshot?.renderedRange) === JSON.stringify(excerpt.snapshot?.renderedRange) &&
		JSON.stringify(ref.snapshot?.diagramRange) === JSON.stringify(excerpt.snapshot?.diagramRange));
	if (duplicate) return { draft };
	const error = excerptSizeError([...draft.references, excerpt]);
	if (error) return { draft, error };
	const order = excerpt.order ?? Math.max(Date.now(), ...draft.references.map(ref => (ref.order ?? 0) + 1));
	return { draft: { ...draft, references: [...draft.references, { ...excerpt, order }] } };
}

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
	onRemoveAllReferences,
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
	onRemoveAllReferences: () => void;
	onFocusSide?: () => void;
	onCompact: () => void;
	focusKey: number;
	queuedDock?: ReactNode;
	onNavigate?: (annotation: ChatAnnotationSummaryItem) => void;
}) {
	const { t } = useTranslation();
	const editor = useRef<ComposerEditorHandle>(null);
	const enterSends = useUiStore((state) => state.chatSendKeyMode === "enter");
	const [concealExpired, setConcealExpired] = useState(false);
	useEffect(() => {
		if (!sending) {
			setConcealExpired(false);
			return;
		}
		const timer = window.setTimeout(() => setConcealExpired(true), 3000);
		return () => window.clearTimeout(timer);
	}, [sending]);
	const filePicker = useRef<HTMLInputElement>(null);
	const compactHandled = useRef(false);
	const [highlighted, setHighlighted] = useState(0);
	const [dismissed, setDismissed] = useState<string>();
	const [trigger, setTrigger] = useState<ComposerTrigger>();
	const commands = useMemo(
		() => [
			...(onFocusSide ? ["btw"].map((name) => ({ name, displayName: name, description: "Opens a side chat", source: "AO" })) : []),
			{
				name: "compact",
				displayName: "compact",
				description: "Compacts this side chat",
				source: "AO",
			},
			...skills.filter((skill) => skill.name !== "compact" && (!onFocusSide || skill.name !== "btw")),
		],
		[skills, onFocusSide],
	);
	const suggestions = trigger?.kind === "skill" && trigger.key !== dismissed ? rankSkills(commands, trigger.query) : [];
	const choose = (value: string) => {
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
					{draft.references.length || draft.attachments.length || files.length ? (
						<div className="flex flex-wrap items-center gap-1.5" data-testid="side-chat-attachments-and-annotations">
							<ChatAnnotationSummary
								annotations={draft.references}
								onSelect={onNavigate}
								onRemove={(ref) => ref.id && onRemoveReference(ref.id)}
								onRemoveAll={onRemoveAllReferences}
								disabled={sending || deliveryPending}
							/>
							{draft.attachments.length ? (
								<ul className="contents" aria-label={t("sideChat.stagedFiles")}>
									{draft.attachments.map((file, index) => (
										<li
											key={file.id}
											className="flex items-center gap-1.5 rounded border border-border px-2 py-1 text-[11px]"
										>
											{file.name}
											<Button
												type="button"
												variant="destructive-ghost"
												size="icon-xs"
												className="-my-1 -mr-1.5"
												aria-label={t("sideChat.remove", { value1: file.name })}
												onClick={() => onRemoveFile(index)}
											>
												<X className="size-3" />
											</Button>
										</li>
									))}
								</ul>
							) : null}
							{files.length ? (
								<ul className="contents" aria-label={t("sideChat.attachedFiles")}>
									{files.map((file, index) => (
										<li
											key={`${file.name}-${index}`}
											className="flex items-center gap-1.5 rounded border border-border bg-background px-2 py-1 text-[11px]"
										>
											<span className="max-w-[120px] truncate" title={file.name}>
												{file.name}
											</span>
											<Button
												type="button"
												variant="destructive-ghost"
												size="icon-xs"
												className="-my-1 -mr-1.5"
												aria-label={t("sideChat.remove", { value1: file.name })}
												onClick={() => onRemoveFile(index + draft.attachments.length)}
											>
												<X className="size-3" />
											</Button>
										</li>
									))}
								</ul>
							) : null}
						</div>
					) : null}
					<ComposerEditor
						ref={editor}
						enterSends={enterSends}
						concealed={sending && !concealExpired}
						label="Side chat question"
						placeholder={
							ready
								? running
									? t("sideChat.nextMessageQueued")
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
							if (chosen?.value === "compact" && key === "Enter" && next.text.trim() === "/compact") {
								compactHandled.current = true;
								onCompact();
								return undefined;
							}
							return chosen?.value;
						}}
						onEnter={(next, event) => {
							if (event.shiftKey) return false;
							if (compactHandled.current) {
								compactHandled.current = false;
								return true;
							}
							if (/^\/btw$/i.test(next.text.trim()) && onFocusSide) {
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
								variant="quiet"
								size="icon-sm"
								shape="pill"
								disabled={!ready || sending}
								aria-label={t("sideChat.attachAFile")}
								onClick={() => filePicker.current?.click()}
								className="shrink-0"
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
							variant="primary"
							size="icon-sm"
							shape="pill"
							aria-label={running && !canSend ? t("sideChat.stopTurn") : t("sideChat.sendMessage")}
							onClick={running && !canSend ? onInterrupt : undefined}
							disabled={running && !canSend ? false : !canSend}
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
	canCreate = true,
) {
	const { t } = useTranslation();
	const translateDraft = useChatDraftTranslation();
	const [sides, setSidesState] = useState<Side[]>([]);
	const sidesRef = useRef(sides);
	const setSides = useCallback((update: Side[] | ((current: Side[]) => Side[])) => {
		const next = typeof update === "function" ? update(sidesRef.current) : update;
		sidesRef.current = next;
		setSidesState(next);
	}, []);
	const [activeId, setActiveId] = useState<string>();
	const [visible, setVisible] = useState(false);
	const [liveSnapshot, setSnapshot] = useState<Snapshot>();
	const retainedSnapshots = useRef(new Map<string, { snapshot: Snapshot; olderPages: Snapshot[] }>());
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
	const tabRowRef = useRef<HTMLElement>(null);
	const hadCloseDialog = useRef(false);
	useEffect(() => {
		const wasOpen = hadCloseDialog.current;
		hadCloseDialog.current = Boolean(confirmClose);
		if (!wasOpen || confirmClose) return;
		const timer = window.setTimeout(() => {
			if (document.activeElement === document.body) {
				tabRowRef.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"], .browser-panel__tab-new')?.focus();
			}
		}, 0);
		return () => window.clearTimeout(timer);
	}, [confirmClose]);
	const [closeWarnings, setCloseWarningsState] = useState<Record<string, "open" | "cleanup_pending" | "unconfirmed">>({});
	const closeWarningsRef = useRef(closeWarnings);
	const setCloseWarnings = useCallback((update: typeof closeWarnings | ((current: typeof closeWarnings) => typeof closeWarnings)) => {
		const next = typeof update === "function" ? update(closeWarningsRef.current) : update;
		closeWarningsRef.current = next;
		setCloseWarningsState(next);
	}, []);
	// The side transcript renders through the main chat's Timeline, which owns
	// selection, source navigation, and scroll follow. Same-side annotations from
	// the composer navigate through it.
	const timelineNavigationRef = useRef<((annotation: ChatAnnotationSummaryItem) => void) | null>(null);
	const [loadingOlder, setLoadingOlder] = useState(false);
	const [error, setError] = useState<string>();
	const [listError, setListError] = useState<string>();
	const [sideError, setSideError] = useState<{ id: string; message: string }>();
	const [pending, setPending] = useState(false);
	const [sendingRevision, setSendingRevision] = useState(0);
	const sendingRef = useRef(new Set<string>());
	const sending = Boolean(activeId && sendingRef.current.has(activeId));
	const [editingTurnId, setEditingTurnId] = useState<string>();
	const [editingText, setEditingText] = useState("");
	const [inputAnswers, setInputAnswers] = useState<Record<string, Record<string, string>>>({});
	const activeIdRef = useRef(activeId);
	activeIdRef.current = activeId;
	const activationRevision = useRef({ id: activeId, revision: 0 });
	if (activationRevision.current.id !== activeId) {
		activationRevision.current = { id: activeId, revision: activationRevision.current.revision + 1 };
	}
	const closedSidesRef = useRef(new Set<string>());
	const [pendingCloses, setPendingCloses] = useState<PendingSideClose[]>([]);
	const closingSidesRef = useRef(new Set<string>());
	const snapshot = liveSnapshot?.side.id === activeId ? liveSnapshot : activeId && closingSidesRef.current.has(activeId) ? retainedSnapshots.current.get(activeId)?.snapshot : undefined;
	const closeRequests = useRef(new Set<string>());
	const closing = Boolean(confirmClose && closeRequests.current.has(confirmClose));
	const [closeRevision, setCloseRevision] = useState(0);
	const lifecycleRevision = useRef(0);
	const listRequests = useRef(0);
	const appliedListRequest = useRef(0);
	const localCreates = useRef(new Map<string, Side>());
	const sideRevisions = useRef(new Map<string, number>());
	const loadingOlderRef = useRef(false);
	const olderGeneration = useRef(0);
	const olderScope = useRef("");
	const nextOlderScope = JSON.stringify([sessionId, activeId, enabled]);
	if (olderScope.current !== nextOlderScope) {
		olderScope.current = nextOlderScope;
		olderGeneration.current++;
		loadingOlderRef.current = false;
	}
	useEffect(() => setLoadingOlder(false), [sessionId, activeId, enabled]);
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const [attachmentRevision, setAttachmentRevision] = useState(0);
	const sessionRef = useRef(sessionId);
	sessionRef.current = enabled ? sessionId : "";
	const creationScope = useMemo(() => ({ active: true, pending: new Set<symbol>() }), [sessionId, enabled, baseUrl, attachmentRevision]);
	const creationScopeRef = useRef(creationScope);
	creationScopeRef.current = creationScope;
	const operationCurrent = useCallback((sideId: string) => {
		const scope = creationScopeRef.current;
		const revision = sideRevisions.current.get(sideId);
		return () => scope.active && creationScopeRef.current === scope && sessionRef.current === sessionId && revision === sideRevisions.current.get(sideId) && !closedSidesRef.current.has(sideId) && !closingSidesRef.current.has(sideId);
	}, [sessionId]);
	useEffect(() => {
		creationScope.active = true;
		setPending(false);
		return () => { creationScope.active = false; };
	}, [creationScope]);
	useEffect(() => {
		let identity: string | undefined;
		let receivedStatus = false;
		let alive = true;
		void aoBridge.daemon.getStatus().then(status => {
			if (alive && !receivedStatus) identity = JSON.stringify([status.state, status.pid, status.port]);
		}).catch(() => undefined);
		const unsubscribe = aoBridge.daemon.onStatus(status => {
			receivedStatus = true;
			const next = JSON.stringify([status.state, status.pid, status.port]);
			if (identity !== next) {
				creationScopeRef.current.active = false;
				setCloseWarnings(warnings => {
					const next = { ...warnings };
					for (const id of closeRequests.current) next[id] = "unconfirmed";
					return next;
				});
				closeRequests.current.clear();
				setConfirmClose(undefined);
				setAttachmentRevision(value => value + 1);
			}
			identity = next;
		});
		return () => { alive = false; unsubscribe(); };
	}, []);
	useEffect(() => {
		sessionRef.current = enabled ? sessionId : "";
		return () => { sessionRef.current = ""; };
	}, [sessionId, enabled]);
	useEffect(() => {
		closedSidesRef.current.clear();
		closingSidesRef.current.clear();
		closeRequests.current.clear();
		retainedSnapshots.current.clear();
		localCreates.current.clear();
		sideRevisions.current.clear();
		setSideError(undefined);
		lifecycleRevision.current++;
		setPendingCloses([]);
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
		setCloseWarnings({});
		setError(undefined);
		setListError(undefined);
		setPending(false);
	}, [sessionId, enabled]);

	const removeSide = useCallback((sideId: string) => {
		lifecycleRevision.current++;
		closedSidesRef.current.add(sideId);
		closingSidesRef.current.delete(sideId);
		localCreates.current.delete(sideId);
		retainedSnapshots.current.delete(sideId);
		setSideError(current => current?.id === sideId ? undefined : current);
		const current = sidesRef.current;
		const index = current.findIndex(side => side.id === sideId);
		const remaining = current.filter(side => side.id !== sideId);
		setSides(remaining);
		setActiveId(selected => selected === sideId ? remaining[Math.min(Math.max(index, 0), remaining.length - 1)]?.id : selected);
		setSnapshot(current => current?.side.id === sideId ? undefined : current);
		setDrafts(current => { const next = { ...current }; delete next[sideId]; return next; });
		setAttachments(current => { const next = { ...current }; delete next[sideId]; return next; });
		setCloseWarnings(current => { const next = { ...current }; delete next[sideId]; return next; });
		setPendingCloses(current => current.filter(side => side.id !== sideId));
		setConfirmClose(current => current === sideId ? undefined : current);
	}, [setSides]);

	const refreshList = useCallback(async (reportErrors = true) => {
		if (!enabled) return;
		const scope = creationScopeRef.current;
		const revision = lifecycleRevision.current;
		const request = ++listRequests.current;
		const current = () => scope.active && creationScopeRef.current === scope && sessionRef.current === sessionId && revision === lifecycleRevision.current && request > appliedListRequest.current;
		try {
			const { data, error: requestError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats", {
				params: { path: { sessionId } },
			});
			if (!current()) return;
			if (requestError) {
				appliedListRequest.current = request;
				if (reportErrors) setListError(apiErrorMessage(requestError));
				return;
			}
			const { pending: closes, closedIds } = await aoBridge.sideChats.getCloseState(sessionId);
			if (!current()) return;
			appliedListRequest.current = request;
			setListError(undefined);
			// Completion is remembered by Electron; a refresh may miss every
			// intermediate cleanup state and may still carry an older open list.
			for (const id of closedIds) if (!closedSidesRef.current.has(id)) removeSide(id);
			setPendingCloses(closes);
			setCloseWarnings(warnings => {
				const next = { ...warnings };
				for (const side of closes) if (!closeRequests.current.has(side.id)) next[side.id] = side.phase;
				return next;
			});
			closingSidesRef.current = new Set([...closes.map((side) => side.id), ...closeRequests.current, ...Object.keys(closeWarningsRef.current).filter(id => closeWarningsRef.current[id] !== "open" && !closedSidesRef.current.has(id))]);
			const nextSides = Array.isArray(data?.sides) ? data.sides.filter((side) => !closedSidesRef.current.has(side.id)) : [];
			for (const [id, side] of localCreates.current) {
				if (nextSides.some(current => current.id === id)) localCreates.current.delete(id);
				else if (!closedSidesRef.current.has(id)) nextSides.push(side);
			}
			for (const side of closes) {
				if (!closedSidesRef.current.has(side.id) && !nextSides.some((current) => current.id === side.id)) nextSides.push(sidesRef.current.find(current => current.id === side.id) ?? { ...side, state: "closing", mainConversationId: "", contextMode: "", hasWork: false, createdAt: "", updatedAt: "" });
			}
			for (const id of closingSidesRef.current) {
				const retained = sidesRef.current.find(side => side.id === id);
				if (retained && !closedSidesRef.current.has(id) && !nextSides.some(side => side.id === id)) nextSides.push(retained);
			}
			setSides(nextSides);
			setActiveId((current) =>
				current && nextSides.some((side) => side.id === current) ? current : nextSides.at(-1)?.id,
			);
		} catch (cause) {
			if (current()) {
				appliedListRequest.current = request;
				if (reportErrors) setListError(apiErrorMessage(cause));
			}
		}
	}, [sessionId, enabled, removeSide, setSides, creationScope]);
	useEffect(() => {
		if (!enabled) return;
		return aoBridge.sideChats.onPendingClosesChanged(() => { void refreshList().catch((cause) => setError(String(cause))); });
	}, [enabled, refreshList]);

	useEffect(() => {
		if (!enabled) return;
		const refresh = () => void refreshList().catch((error) => setError(String(error)));
		refresh();
		const timer = window.setInterval(refresh, 2000);
		return () => window.clearInterval(timer);
	}, [refreshList, enabled]);

	useEffect(() => {
		if (liveSnapshot && liveSnapshot.side.id === activeId && !closingSidesRef.current.has(activeId!)) {
			retainedSnapshots.current.set(liveSnapshot.side.id, { snapshot: liveSnapshot, olderPages });
		}
	}, [liveSnapshot, olderPages, activeId]);

	useEffect(() => {
		setOlderPages([]);
		loadingOlderRef.current = false;
		setLoadingOlder(false);
		if (!enabled || !activeId) {
			setSnapshot(undefined);
			return;
		}
		if (closingSidesRef.current.has(activeId) || closedSidesRef.current.has(activeId)) return;
		let alive = true;
		const available = operationCurrent(activeId);
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
			if (!alive || !available() || requestNumber < lastApplied) return;
			lastApplied = requestNumber;
			if (requestError) {
				setSideError({ id: activeId, message: apiErrorMessage(requestError) });
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
							...new Map(
								[...(current.messages ?? []), ...(next.messages ?? [])].map((message) => [message.id, message]),
							).values(),
						],
						activities: [
							...new Map(
								[...(current.activities ?? []), ...(next.activities ?? [])].map((activity) => [activity.id, activity]),
							).values(),
						],
					};
				});
				setSideError(current => current?.id === activeId ? undefined : current);
			}
		};
		const refreshSafely = () =>
			void refresh().catch((error) => {
				if (alive && available()) setSideError({ id: activeId, message: String(error) });
			});
		refreshSafely();
		let stream: EventSource | undefined;
		if (baseUrl && typeof EventSource !== "undefined") {
			stream = new EventSource(
				`${eventStreamBaseUrl(baseUrl)}/api/v1/sessions/${encodeURIComponent(sessionId)}/conversation/side-chats/${encodeURIComponent(activeId)}/events`,
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
	}, [sessionId, activeId, baseUrl, snapshotRefreshKey, enabled, closeRevision, operationCurrent, creationScope]);

	const loadOlder = useCallback(async () => {
		if (!activeId || !snapshot || loadingOlderRef.current || closingSidesRef.current.has(activeId)) return;
		const oldest = olderPages.at(-1)?.turns?.[0]?.createdAt ?? snapshot.turns?.[0]?.createdAt;
		if (!oldest) return;
		loadingOlderRef.current = true;
		setLoadingOlder(true);
		const generation = ++olderGeneration.current;
		const scope = olderScope.current;
		const available = operationCurrent(activeId);
		const current = () => available() && olderGeneration.current === generation && olderScope.current === scope;
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
			if (!current()) return;
			if (requestError) {
				setSideError({ id: activeId, message: apiErrorMessage(requestError) });
				return;
			}
			if (data?.snapshot && current()) setOlderPages((pages) => [...pages, data.snapshot]);
		} catch (cause) {
			if (current()) setSideError({ id: activeId, message: apiErrorMessage(cause) });
		} finally {
			if (current()) {
				loadingOlderRef.current = false;
				setLoadingOlder(false);
			}
		}
	}, [sessionId, activeId, snapshot, olderPages, operationCurrent]);

	useEffect(() => {
		if (!activeId || drafts[activeId] !== undefined || closingSidesRef.current.has(activeId) || closedSidesRef.current.has(activeId)) return;
		const available = operationCurrent(activeId);
		void readSideChatDraft(sessionId, activeId)
			.then(({ data }) => {
				if (!available()) return;
				if (data)
					setDrafts((current) => ({
						...current,
						[activeId]: current[activeId] ?? parseSideChatDraft(data.contentJson),
					}));
			})
			.catch((error) => {
				if (available()) setSideError({ id: activeId, message: String(error) });
			});
	}, [sessionId, activeId, drafts, sendingRevision, closeRevision, operationCurrent]);

	const create = useCallback(
		async (excerpt?: ChatDraftExcerptReference, forceNew = false, idempotencyKey: string = crypto.randomUUID(),
			options?: { isCurrent?: () => boolean; shouldActivate: () => boolean }) => {
			if (!enabled) throw new Error("Side chats are currently available only for local Orchestrator-managed sessions.");
			if (!canCreate) throw new Error("The current provider does not support resumable side chats.");
			if (excerpt) {
				const sizeError = excerptSizeError([excerpt]);
				if (sizeError) throw new Error(sizeError);
			}
			const owned = () => creationScope.active && creationScopeRef.current === creationScope && sessionRef.current === sessionId;
			const current = () => owned() && (options?.isCurrent?.() ?? true);
			if (!current()) throw new Error("Side chat is no longer active.");
			const activation = activationRevision.current.revision;
			const request = Symbol();
			creationScope.pending.add(request);
			setPending(true);
			setError(undefined);
			try {
				const { data, error: requestError } = await apiClient.POST(
					"/api/v1/sessions/{sessionId}/conversation/side-chats",
					{
						params: { path: { sessionId } },
						body: {
							idempotencyKey,
							forceNew,

							reference: excerpt
								? {
										conversationId: excerpt.conversationId,
										messageId: excerpt.messageId,
										revision: excerpt.revision,
										text: excerpt.text,
										snapshot: excerpt.snapshot,
									}
								: undefined,
						},
					},
				);
				if (requestError) throw requestError;
				const available = () => current() && !closedSidesRef.current.has(data.side.id) && !closingSidesRef.current.has(data.side.id);
				if (!available())
					throw new Error("Side chat is no longer active.");
				await aoBridge.sideChats.capture().catch(() => undefined);
				if (!available()) throw new Error("Side chat is no longer active.");
				if (excerpt) {
					// An unconfirmed create can recover an existing, inactive side. Load
					// its draft before appending, and save without focusing any composer.
					let draft = draftsRef.current[data.side.id];
					if (!draft) {
						const { data: saved, error: draftError } = await readSideChatDraft(sessionId, data.side.id);
						if (draftError) throw draftError;
						if (!available()) throw new Error("Side chat is no longer active.");
						draft = draftsRef.current[data.side.id] ?? parseSideChatDraft(saved?.contentJson ?? "");
					}
					// Commit a functional merge before saving: another side (or this
					// one) may have an edit queued in the same React batch as the read.
					const loadedDraft = draft;
					let nextDraft = loadedDraft;
					let appendError: string | undefined;
					flushSync(() => setDrafts(currentDrafts => {
						const latest = currentDrafts[data.side.id] ?? loadedDraft;
						const result = appendReference(latest, excerpt);
						appendError = result.error;
						if (appendError) return currentDrafts;
						nextDraft = result.draft;
						return { ...currentDrafts, [data.side.id]: nextDraft };
					}));
					if (!available()) throw new Error("Side chat is no longer active.");
					if (appendError) throw new Error(appendError);
					const { error: draftError } = await saveSideChatDraft(sessionId, data.side.id, JSON.stringify(nextDraft), available);
					if (draftError) throw draftError;
					if (!available()) throw new Error("Side chat is no longer active.");
				}
				lifecycleRevision.current++;
				localCreates.current.set(data.side.id, data.side);
				setSides((current) =>
					current.some((side) => side.id === data.side.id)
						? current.map((side) => (side.id === data.side.id ? data.side : side))
						: [...current, data.side],
				);
				if (!options || (activationRevision.current.revision === activation && options.shouldActivate())) {
					setActiveId(data.side.id);
					setVisible(true);
					setFocusKey((key) => key + 1);
				}
				return { id: data.side.id };
			} catch (cause) {
				if (current()) setError(apiErrorMessage(cause));
				throw cause;
			} finally {
				creationScope.pending.delete(request);
				if (owned()) setPending(creationScope.pending.size > 0);
			}
		},
		[sessionId, enabled, canCreate, creationScope],
	);

	const updateDraft = useCallback((sideId: string, text: string) => {
		if (closingSidesRef.current.has(sideId) || closedSidesRef.current.has(sideId)) return;
		setDrafts((current) => ({
			...current,
			[sideId]: { ...(current[sideId] ?? emptySideChatDraft()), text },
		}));
	}, []);
	const replaceDraft = useCallback(
		async (sideId: string, draft: SideChatDraft) => {
			if (closingSidesRef.current.has(sideId) || closedSidesRef.current.has(sideId)) throw new Error("Side chat is closing.");
			const available = operationCurrent(sideId);
			const next = {
				...draft,
				pendingDelivery: draftsRef.current[sideId]?.pendingDelivery,
				pendingCommand: draftsRef.current[sideId]?.pendingCommand,
				lastHandoffDelivery: draftsRef.current[sideId]?.lastHandoffDelivery,
			};
			// Apply the edit before awaiting I/O so its response cannot overwrite a newer receipt.
			draftsRef.current = { ...draftsRef.current, [sideId]: next };
			setDrafts((current) => ({ ...current, [sideId]: next }));
			setAttachments((current) => ({ ...current, [sideId]: [] }));
			setFocusKey((key) => key + 1);
			const { error: requestError } = await saveSideChatDraft(sessionId, sideId, JSON.stringify(next), available);
			if (requestError) throw requestError;
			if (available()) await aoBridge.sideChats.capture();
		},
		[sessionId],
	);

	const addReference = useCallback(async (sideId: string, excerpt: ChatDraftExcerptReference) => {
		if (closingSidesRef.current.has(sideId) || closedSidesRef.current.has(sideId)) throw new Error("Side chat is closing.");
		const available = operationCurrent(sideId);
		const draft = draftsRef.current[sideId] ?? emptySideChatDraft();
		const result = appendReference(draft, excerpt);
		if (result.error) throw new Error(result.error);
		draftsRef.current = { ...draftsRef.current, [sideId]: result.draft };
		setDrafts(current => ({ ...current, [sideId]: result.draft }));
		const { error: requestError } = await saveSideChatDraft(sessionId, sideId, JSON.stringify(result.draft), available);
		if (!available()) return;
		if (requestError) throw requestError;
		setFocusKey(key => key + 1);
	}, [sessionId, operationCurrent]);

	const attachFiles = useCallback(
		async (sideId: string, files: File[]) => {
			if (files.length === 0 || closingSidesRef.current.has(sideId) || closedSidesRef.current.has(sideId)) return;
			const available = operationCurrent(sideId);
			try {
				const payloads = await Promise.all(files.map(sideFilePayload));
				const paths = await stageAttachments(payloads);
				if (!available()) return;
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
				if (available()) setSideError({ id: sideId, message: apiErrorMessage(cause) });
			}
		},
		[stageAttachments, operationCurrent],
	);

	useEffect(() => {
		if (!activeId || drafts[activeId] === undefined || closingSidesRef.current.has(activeId)) return;
		const available = operationCurrent(activeId);
		const draft = drafts[activeId];
		const save = () => {
			if (!available() || (sendingRef.current.has(activeId) && activeIdRef.current === activeId && sessionRef.current === sessionId)) return;
			const currentDraft = draftsRef.current[activeId] ?? draft;
			void saveSideChatDraft(sessionId, activeId, JSON.stringify(currentDraft), available)
				.then(({ error: requestError }) => {
					if (requestError) throw requestError;
					if (available()) return aoBridge.sideChats.capture();
					return;
				})
				.catch((cause) => {
					if (available())
						setSideError({ id: activeId, message: apiErrorMessage(cause) });
				});
		};
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
	}, [sessionId, activeId, drafts, sendingRevision, closeRevision, operationCurrent]);

	const cancelHandoff = useCallback(async (sideId: string) => {
		const pending = draftsRef.current[sideId]?.pendingCommand;
		if (!pending || sendingRef.current.has(sideId) || closedSidesRef.current.has(sideId) || closingSidesRef.current.has(sideId)) return;
		sendingRef.current.add(sideId);
		setSendingRevision(version => version + 1);
		const command = { ...pending, cancelRequested: true };
		const available = operationCurrent(sideId);
		const apply = (remove: boolean) => {
			if (!available()) throw new Error("Side chat is no longer active.");
			const current = draftsRef.current[sideId];
			if (!current || (current.pendingCommand && current.pendingCommand.creationKey !== command.creationKey)) throw new Error("Side chat handoff changed.");
			const { pendingCommand: _, ...rest } = current;
			const next = remove ? rest : { ...rest, pendingCommand: command };
			draftsRef.current = { ...draftsRef.current, [sideId]: next };
			setDrafts(current => ({ ...current, [sideId]: next }));
			return next;
		};
		const persist = async (draft: SideChatDraft) => {
			const saved = await saveSideChatDraft(sessionId, sideId, JSON.stringify(draft), available);
			if (saved.error) throw saved.error;
			await aoBridge.sideChats.capture();
		};
		try {
			// A remount/crash after this capture can only finish cancellation, never send.
			await persist(apply(false));
			await persist(apply(true));
			if (available()) setError(undefined);
		} catch (cause) {
			if (available()) {
				if (!draftsRef.current[sideId]?.pendingCommand || draftsRef.current[sideId].pendingCommand?.creationKey === command.creationKey) apply(false); // Preserve newer edits and cancellation intent.
				setError(apiErrorMessage(cause));
			}
		} finally {
			sendingRef.current.delete(sideId);
			setSendingRevision(version => version + 1);
		}
	}, [sessionId]);

	const send = useCallback(
		async (
			sideId: string,
			text: string,
			extraAttachments: { mimeType: string; data: string }[] = [],
			overrideReferences?: ChatDraftExcerptReference[],
			propagateError = false,
			handoffClientMessageId?: string,
			preparedReceipt?: SideDeliveryReceipt,
			canDispatch?: () => boolean,
		): Promise<void> => {
			if (!preparedReceipt && draftsRef.current[sideId]?.pendingCommand?.cancelRequested) { await cancelHandoff(sideId); return; }
			if (closingSidesRef.current.has(sideId) || closedSidesRef.current.has(sideId)) {
				if (propagateError) throw new Error("Side chat is closing.");
				return;
			}
			const btw = preparedReceipt ? null : /^\/btw(?:\s+|$)/i.exec(text);
			if (!preparedReceipt && (btw || (!text && draftsRef.current[sideId]?.pendingCommand))) {
				if (sendingRef.current.has(sideId)) return;
				sendingRef.current.add(sideId);
				setSendingRevision((version) => version + 1);
				let command = draftsRef.current[sideId]?.pendingCommand;
				const available = operationCurrent(sideId);
				const persist = async () => {
					if (!available()) throw new Error("Side chat is no longer active.");
					const next = { ...(draftsRef.current[sideId] ?? emptySideChatDraft()), pendingCommand: command };
					draftsRef.current = { ...draftsRef.current, [sideId]: next };
					setDrafts((current) => ({ ...current, [sideId]: next }));
					const saved = await saveSideChatDraft(sessionId, sideId, JSON.stringify(next), available);
					if (saved.error) throw saved.error;
					await aoBridge.sideChats.capture();
				};
				try {
					if (!Object.hasOwn(draftsRef.current, sideId)) {
						const loaded = await readSideChatDraft(sessionId, sideId);
						if (loaded.error) throw loaded.error;
						if (!available()) return;
						draftsRef.current = { ...draftsRef.current, [sideId]: draftsRef.current[sideId] ?? parseSideChatDraft(loaded.data?.contentJson ?? "") };
						command = draftsRef.current[sideId].pendingCommand;
					}
					const draft = draftsRef.current[sideId] ?? emptySideChatDraft();
					if (command?.cancelRequested) {
						sendingRef.current.delete(sideId);
						await cancelHandoff(sideId);
						return;
					}
					if (draft.pendingDelivery || (command && text && text !== command.receipt.submitted.text))
						throw new Error("Retry the original handoff before sending another question.");
					if (!command) {
						const submitted = { text, attachments: [...draft.attachments], references: fitExcerptContext((overrideReferences ?? draft.references).map(upgradeExcerpt), text, LOCAL_CHAT_MESSAGE_BYTES) };
						const parentId = sides.find((side) => side.id === sideId)?.mainConversationId;
						if (submitted.references.some((ref) => ref.conversationId !== parentId))
							throw new Error("References from another side cannot be transferred. Remove them before creating a new side.");
						const images = [...extraAttachments, ...(await Promise.all((attachments[sideId] ?? []).filter(({ file }) => /^image\/(png|jpeg|gif|webp)$/i.test(file.type)).map(({ file }) => sideFilePayload(file))))];
						const staged = extraAttachments.length ? await stageAttachments(extraAttachments) : [];
						if (staged.length !== extraAttachments.length) throw new Error("Could not stage every attached file.");
						const clientMessageId = crypto.randomUUID();
						command = {
							creationKey: crypto.randomUUID(),
							receipt: {
								clientMessageId, submitted,
								request: {
									clientMessageId,
									text: text.slice(btw?.[0].length ?? 0).trim(),
									resources: [...submitted.attachments.map((file) => file.path), ...staged].map((path) => ({ uri: path, name: path.split(/[\\/]/).at(-1) || "Attachment" })),
									attachments: nativeImages ? images : [],
									references: submitted.references.map(({ conversationId, messageId, revision, text, snapshot, id, order }) => ({ conversationId, messageId, revision, text, snapshot, id, order })),
								},
							},
						};
					}
					await persist();
					if (!available()) return;
					if (!command.destinationId) {
						const destination = await create(undefined, true, command.creationKey);
						command = { ...command, destinationId: destination.id };
						await persist();
					}
					if (!available()) return;
					if (!command.accepted) {
						const request = command.receipt.request;
						if (request.text || request.resources?.length || request.attachments?.length || request.references?.length)
							await send(command.destinationId!, request.text, [], [], true, command.receipt.clientMessageId, command.receipt, available);
						command = { ...command, accepted: true };
						await persist();
					}
					if (!available()) return;
					const current = draftsRef.current[sideId];
					const original = command.receipt.submitted;
					const { pendingCommand: _, ...rest } = current;
					const next = {
						...rest,
						text: current.text === original.text ? "" : current.text,
						attachments: current.attachments.filter((file) => !original.attachments.some((submitted) => JSON.stringify(submitted) === JSON.stringify(file))),
						references: current.references.filter((reference) => !original.references.some((submitted) => JSON.stringify(submitted) === JSON.stringify(reference))),
					};
					draftsRef.current = { ...draftsRef.current, [sideId]: next };
					setDrafts((current) => ({ ...current, [sideId]: next }));
					const saved = await saveSideChatDraft(sessionId, sideId, JSON.stringify(next), available);
					if (saved.error) {
						await persist();
						throw saved.error;
					}
					setAttachments((current) => ({
						...current,
						[sideId]: (current[sideId] ?? []).filter(({ path }) => !original.attachments.some((submitted) => submitted.path === path)),
					}));
					await aoBridge.sideChats.capture().catch(() => undefined);
				} catch (cause) {
					if (available()) setError(apiErrorMessage(cause));
					if (propagateError) throw cause;
				} finally {
					sendingRef.current.delete(sideId);
					setSendingRevision((version) => version + 1);
				}
				return;
			}
			if (draftsRef.current[sideId]?.pendingCommand) {
				const cause = new Error("Retry the original handoff before sending another question.");
				setError(cause.message);
				if (propagateError) throw cause;
				return;
			}

			if (sendingRef.current.has(sideId)) {
				if (propagateError) throw new Error("Side chat delivery is still in progress. Try again after it finishes.");
				return;
			}
			const available = operationCurrent(sideId);
			if (!available()) return;
			sendingRef.current.add(sideId);
			setSendingRevision((version) => version + 1);
			setError(undefined);
			let receipt = draftsRef.current[sideId]?.pendingDelivery ?? preparedReceipt;
			try {
				if (!Object.hasOwn(draftsRef.current, sideId)) {
					const { data, error } = await readSideChatDraft(sessionId, sideId);
					if (error) throw error;
					if (!available()) return;
					const loaded = parseSideChatDraft(data?.contentJson ?? "");
					const current = draftsRef.current[sideId] ?? loaded;
					draftsRef.current = { ...draftsRef.current, [sideId]: current };
					receipt = current.pendingDelivery ?? preparedReceipt;
				}
				const draft = draftsRef.current[sideId] ?? emptySideChatDraft();
				if (draft.pendingCommand) throw new Error("Retry the original handoff before sending another question.");
				if (!receipt && handoffClientMessageId && draft.lastHandoffDelivery?.clientMessageId === handoffClientMessageId)
					receipt = draft.lastHandoffDelivery;
				if (receipt && handoffClientMessageId && receipt.clientMessageId !== handoffClientMessageId)
					throw new Error("This side chat has a different pending delivery.");
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

					const clientMessageId = handoffClientMessageId ?? crypto.randomUUID();
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
							references: fitExcerptContext((overrideReferences ?? draft.references).map(upgradeExcerpt), text, LOCAL_CHAT_MESSAGE_BYTES).map((ref) => ({
								conversationId: ref.conversationId,
								messageId: ref.messageId,
								revision: ref.revision,
								text: ref.text, snapshot: ref.snapshot, id: ref.id, order: ref.order,
							})),
						},
					};
				}
				if (!available()) return;
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
				const saved = await saveSideChatDraft(sessionId, sideId, JSON.stringify(pending), available);
				if (saved.error) throw saved.error;
				await aoBridge.sideChats.capture();
				if (!available() || (canDispatch && !canDispatch())) {
					if (propagateError) throw new Error("Side chat is no longer active.");
					return;
				}
				const { error: requestError, response } = await apiClient.POST(
					"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/messages",
					{
						params: { path: { sessionId, sideId } },
						body: frozen.request,
					},
				);
				if (!available()) return;
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
				if (!available()) return;
				const d = draftsRef.current[sideId];
				if (d?.pendingDelivery === frozen) {
					const { pendingDelivery: _, ...rest } = d;
					const unchanged =
						d.text === frozen.submitted.text &&
						JSON.stringify(d.attachments) === JSON.stringify(frozen.submitted.attachments) &&
						JSON.stringify(d.references) === JSON.stringify(frozen.submitted.references);
					const next: SideChatDraft = {
						...(unchanged ? emptySideChatDraft() : rest),
						lastHandoffDelivery: handoffClientMessageId ? frozen : d.lastHandoffDelivery,
					};
					draftsRef.current = { ...draftsRef.current, [sideId]: next };
					setDrafts((current) => ({ ...current, [sideId]: next }));
					const saved = await saveSideChatDraft(sessionId, sideId, JSON.stringify(next), available);
					if (saved.error) throw saved.error;
					await aoBridge.sideChats.capture().catch(() => undefined);
				}

				setAttachments((current) => (current[sideId] === files ? { ...current, [sideId]: [] } : current));
				// The first accepted question may have named this tab. A list refresh
				// failure must not turn an accepted delivery into a failed send.
				void refreshList(false).catch(() => undefined);
			} catch (cause) {
				if (available()) setError(apiErrorMessage(cause));
				if (propagateError) throw cause;
			} finally {
				sendingRef.current.delete(sideId);
				setSendingRevision((version) => version + 1);
			}
		},
		[sessionId, attachments, stageAttachments, nativeImages, create, sides, cancelHandoff, refreshList],
	);

	const close = useCallback(
		async (sideId: string) => {
			if (closeRequests.current.has(sideId) || closedSidesRef.current.has(sideId)) return;
			const scope = creationScopeRef.current;
			const current = () => scope.active && creationScopeRef.current === scope && sessionRef.current === sessionId;
			closeRequests.current.add(sideId);
			closingSidesRef.current.add(sideId);
			sideRevisions.current.set(sideId, (sideRevisions.current.get(sideId) ?? 0) + 1);
			lifecycleRevision.current++;
			setError(undefined);
			setSideError(error => error?.id === sideId ? undefined : error);
			setCloseWarnings(warnings => { const next = { ...warnings }; delete next[sideId]; return next; });
			setCloseRevision(value => value + 1);
			let outcome: SideCloseOutcome = "unconfirmed";
			let failure: unknown;
			let rejected = false;
			try {
				try {
					await aoBridge.sideChats.capture(sideId);
					if (!current()) return;
					const { error: requestError, response } = await apiClient.DELETE(
						"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}",
						{ params: { path: { sessionId, sideId } } },
					);
					if (!current()) return;
					if (!requestError) {
						// Failed bookkeeping cannot undo HTTP shutdown proof, but an explicit
						// attachment mismatch needs reconciliation with the replacement daemon.
						outcome = "closed";
						const confirmed = await aoBridge.sideChats.confirmClose(sideId).catch(() => undefined);
						if (confirmed === "unconfirmed") outcome = "unconfirmed";
					} else if (response.status === 404) {
						outcome = await aoBridge.sideChats.confirmClose(sideId, true);
					} else {
						if ([400, 401, 403, 405, 409, 422].includes(response.status)) {
							await aoBridge.sideChats.capture(sideId, true);
							rejected = true;
						} else await aoBridge.sideChats.capture().catch(() => undefined);
						throw apiErrorException(requestError);
					}
				} catch (cause) {
					failure = cause;
				}
				if (!current()) return;
				if (closedSidesRef.current.has(sideId)) { outcome = "closed"; return; }
				if (outcome === "closed") { removeSide(sideId); return; }
				// Keep the request marker through reconciliation: a concurrent list
				// refresh must not unlock a side whose capture failed before pinning.
				let state: SideCloseState | undefined;
				try { state = await aoBridge.sideChats.getCloseState(sessionId); } catch { /* keep the local close intent */ }
				if (!current()) return;
				if (state?.closedIds.includes(sideId)) { outcome = "closed"; removeSide(sideId); return; }
				const closes = state?.pending;
				const pending = closes?.find(side => side.id === sideId);
				if (closes) setPendingCloses(closes);
				if (pending?.phase === "cleanup_pending") outcome = "cleanup_pending";
				const stillOpen = rejected && closes !== undefined && !pending;
				if (stillOpen) closingSidesRef.current.delete(sideId);
				setCloseWarnings(warnings => ({ ...warnings, [sideId]: stillOpen ? "open" : outcome === "cleanup_pending" ? "cleanup_pending" : "unconfirmed" }));
				throw failure ?? new Error("Side chat shutdown is not confirmed.");
			} finally {
				if (current()) {
					closeRequests.current.delete(sideId);
					lifecycleRevision.current++;
					setCloseRevision(value => value + 1);
					if (outcome !== "closed") void refreshList().catch(() => undefined);
				}
			}
		},
		[sessionId, refreshList, removeSide],
	);

	const interrupt = useCallback(
		async (sideId: string) => {
			const available = operationCurrent(sideId);
			if (!available()) return;
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/interrupt",
				{
					params: { path: { sessionId, sideId } },
				},
			);
			if (!available()) return;
			if (requestError) setSideError({ id: sideId, message: apiErrorMessage(requestError) });
		},
		[sessionId],
	);
	const compact = useCallback(
		async (sideId: string) => {
			const available = operationCurrent(sideId);
			if (!available()) return;
			setError(undefined);
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/compact",
				{
					params: { path: { sessionId, sideId } },
				},
			);
			if (!available()) return;
			if (requestError) {
				setSideError({ id: sideId, message: apiErrorMessage(requestError) });
				return;
			}
			updateDraft(sideId, "");
		},
		[sessionId, updateDraft],
	);
	const resolveApproval = useCallback(
		async (sideId: string, requestId: string, decisionId: string) => {
			const available = operationCurrent(sideId);
			if (!available()) return;
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/approvals/{requestId}/resolve",
				{
					params: { path: { sessionId, sideId, requestId } },
					body: { decisionId },
				},
			);
			if (!available()) return;
			if (requestError) setSideError({ id: sideId, message: apiErrorMessage(requestError) });
		},
		[sessionId],
	);
	const resolveInput = useCallback(
		async (sideId: string, requestId: string, action: "accept" | "decline" | "cancel") => {
			const available = operationCurrent(sideId);
			if (!available()) return;
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
			if (!available()) return;
			if (requestError) setSideError({ id: sideId, message: apiErrorMessage(requestError) });
		},
		[sessionId, inputAnswers],
	);
	const updateSettings = useCallback(
		async (sideId: string, model: string, effort: string) => {
			const available = operationCurrent(sideId);
			if (!available()) return;
			const { error: requestError } = await apiClient.PATCH(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/settings",
				{
					params: { path: { sessionId, sideId } },
					body: { model, effort },
				},
			);
			if (!available()) return;
			if (requestError) setSideError({ id: sideId, message: apiErrorMessage(requestError) });
		},
		[sessionId],
	);
	const retry = useCallback(
		async (sideId: string, turnId: string) => {
			const available = operationCurrent(sideId);
			if (!available()) return;
			const { error: requestError } = await apiClient.POST(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/retry",
				{
					params: { path: { sessionId, sideId, turnId } },
				},
			);
			if (!available()) return;
			if (requestError) setSideError({ id: sideId, message: apiErrorMessage(requestError) });
		},
		[sessionId],
	);
	const saveQueuedEdit = useCallback(
		async (sideId: string) => {
			const available = operationCurrent(sideId);
			if (!available()) return;
			if (!editingTurnId || !editingText.trim()) return;
			const { error: requestError } = await apiClient.PATCH(
				"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}",
				{
					params: { path: { sessionId, sideId, turnId: editingTurnId } },
					body: { text: editingText, clientMessageId: "" },
				},
			);
			if (!available()) return;
			if (requestError) {
				setSideError({ id: sideId, message: apiErrorMessage(requestError) });
				return;
			}
			setEditingTurnId(undefined);
		},
		[sessionId, editingTurnId, editingText],
	);
	const currentActiveId = sides.some((side) => side.id === activeId && side.sessionId === sessionId)
		? activeId
		: undefined;
	// The main chat's Timeline renders the side transcript, so live status,
	// "Worked for", and scroll follow behave exactly as they do in the main chat.
	// Stable per side: the Timeline's draft effects depend on this identity.
	const timelineDraftScope = useMemo(
		() => (activeId ? { sessionId: `side:${activeId}`, incarnation: activeId } : undefined),
		[activeId],
	);
	const readOnly = Boolean(activeId && closingSidesRef.current.has(activeId));
	const warning = activeId ? closeWarnings[activeId] : undefined;
	const activeError = error || listError || (sideError && sideError.id === activeId ? sideError.message : undefined);
	const conversation = useMemo(
		() => (snapshot && snapshot.side.id === activeId ? sideConversationSnapshot(snapshot, readOnly ? retainedSnapshots.current.get(snapshot.side.id)?.olderPages ?? [] : olderPages) : undefined),
		[snapshot, olderPages, activeId, readOnly],
	);
	const navigateAnnotation = useCallback(
		(annotation: ChatAnnotationSummaryItem) => {
			if (annotation.conversationId === activeId) timelineNavigationRef.current?.(annotation);
			else onNavigate?.(annotation);
		},
		[activeId, onNavigate],
	);
	const {
		scrollRef: tabStripRef,
		scrollToEnd,
	} = useTabScrollEdges([sides.length, visible]);
	const previousTabCount = useRef(sides.length);
	useEffect(() => {
		if (sides.length > previousTabCount.current) scrollToEnd();
		previousTabCount.current = sides.length;
	}, [sides.length, scrollToEnd]);
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
		const id = renameId,
			label = renameText?.trim();
		setRenameId(undefined);
		setRenameText(undefined);
		if (!label || label === sides.find((side) => side.id === id)?.label) return;
		const available = operationCurrent(id);
		if (!available()) return;
		const { error: requestError } = await apiClient.PATCH(
			"/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/label",
			{ params: { path: { sessionId, sideId: id } }, body: { label } },
		);
		if (!available()) return;
		if (requestError) {
			setSideError({ id, message: apiErrorMessage(requestError) });
			return;
		}
		await refreshList();
	};

	const tabBar = enabled ? (
		<header ref={tabRowRef} className="browser-panel__tab-row side-chat__tab-row">
			<div className="browser-panel__tab-region">
				<div
					ref={tabStripRef}
					role="tablist"
					aria-label={t("sideChat.tabs")}
					onKeyDown={handleTabListKeyDown}
					className="browser-panel__tab-strip edge-fade edge-fade-x"
				>
					{sides.map((side) => {
						const tab = (
							<div
								key={side.id}
								className={cn("browser-panel__tab", side.id === currentActiveId && "browser-panel__tab--active")}
							>
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
									<Pressable
										role="tab"
										aria-selected={side.id === currentActiveId}
										aria-busy={closeRequests.current.has(side.id) || undefined}
										tabIndex={side.id === currentActiveId ? 0 : -1}
										className="browser-panel__tab-select"
										onClick={(event) => {
											if (event.detail > 1) return;
											setActiveId(side.id);
											setFocusKey(0);
										}}
										aria-keyshortcuts="F2"
										onDoubleClick={(event) => {
											event.preventDefault();
											if (!closingSidesRef.current.has(side.id)) beginRename(side);
										}}
										onKeyDown={(event) => {
											if (event.key === "F2") {
												event.preventDefault();
												if (!closingSidesRef.current.has(side.id)) beginRename(side);
											}
										}}
										title={side.label}
									>
										{closeRequests.current.has(side.id) ? <Loader2 className="browser-panel__tab-icon animate-spin" aria-hidden="true" /> : <MessageSquare className="browser-panel__tab-icon" aria-hidden="true" />}
										<span className="browser-panel__tab-title">{side.label?.trim() || t("sideChat.defaultLabel")}</span>
										{closeRequests.current.has(side.id) ? <span role="status" className="sr-only">{t("sideChat.deleting")}</span> : null}
									</Pressable>
								)}
								<Pressable
									hidden={renameId === side.id || closeRequests.current.has(side.id) || pendingCloses.some(pending => pending.id === side.id && pending.phase === "cleanup_pending")}
									className="browser-panel__tab-close"
									aria-label={t("sideChat.closeNamed", { name: side.label })}
									onClick={() => {
										setConfirmClose(side.id);
									}}
								>
									<X className="size-3.5" aria-hidden="true" />
								</Pressable>
							</div>
						);
						if (renameId === side.id || pendingCloses.some((pending) => pending.id === side.id)) return tab;
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
			</div>
			<Pressable
				type="button"
				className="browser-panel__tab-new"
				disabled={!canCreate || pending}
				aria-label={t("sideChat.newSideChat")}
				onClick={() => void create(undefined, true).catch(() => undefined)}
			>
				<Plus className="size-3.5" />
			</Pressable>
		</header>
	) : null;
	const closeDialog = (
		<ConfirmDialog
			open={Boolean(confirmClose)}
			title={t("sideChat.deleteConfirm")}
			description={t("sideChat.deleteExplanation")}
			confirmLabel={t("sideChat.delete")}
			cancelLabel={t("confirm.cancel")}
			busy={closing}
			onOpenChange={(open) => {
				if (!open && !closing) setConfirmClose(undefined);
			}}
			onConfirm={() => {
				if (!confirmClose || closeRequests.current.has(confirmClose)) return;
				const scope = creationScopeRef.current;
				const current = () => scope.active && creationScopeRef.current === scope;
				void close(confirmClose)
					.catch(() => undefined)
					.finally(() => {
						if (!current()) return;
						setConfirmClose(selected => selected === confirmClose ? undefined : selected);
					});
			}}
		/>
	);
	// Like the main chat, the transcript shows answered requests and the pending
	// ones wait beside the composer, where the reader acts on them.
	const pendingInteractions = (snapshot?.side.id === activeId ? snapshot?.activities ?? [] : []).filter(
		(activity) =>
			activity.status === "pending" &&
			activity.requestId &&
			(activity.kind === "approval" || ((activity.kind === "user_input" || activity.kind === "input") && activity.input)),
	);
	const renderPendingInteraction = (activity: (typeof pendingInteractions)[number]) => {
		if (!activeId) return null;
		const requestId = activity.requestId!;
		if (activity.kind === "approval")
			return (
				<ApprovalCard
					key={activity.id}
					activity={sideConversationActivity(activity)}
					onDecide={(id, decisionId) => void resolveApproval(activeId, id, decisionId)}
				/>
			);
		const input = activity.input!;
		return (
			<div key={activity.id} className="space-y-2 rounded-lg border border-border px-3 py-2 text-sm">
				<p>{input.message}</p>
				{input.url && /^https?:\/\//i.test(input.url) ? (
					<a className="underline" href={input.url} target="_blank" rel="noreferrer">
						{t("sideChat.openRequestedUrl")}
					</a>
				) : null}
				{Object.keys((input.schema?.properties ?? {}) as Record<string, unknown>).map((key) => (
					<label key={key} className="block">
						{key}
						<input
							className="ml-2 rounded border border-border bg-background p-1"
							value={inputAnswers[requestId]?.[key] ?? ""}
							onChange={(event) =>
								setInputAnswers((current) => ({
									...current,
									[requestId]: { ...current[requestId], [key]: event.target.value },
								}))
							}
						/>
					</label>
				))}
				<div className="flex gap-2">
					<Button type="button" size="sm" onClick={() => void resolveInput(activeId, requestId, "accept")}>
						{t("sideChat.submit")}
					</Button>
					<Button type="button" size="sm" variant="secondary" onClick={() => void resolveInput(activeId, requestId, "decline")}>
						{t("sideChat.decline")}
					</Button>
				</div>
			</div>
		);
	};
	const panel =
		enabled && currentActiveId && visible ? (
			<aside
				data-chat-find-scope=""
				aria-label={t("sideChat.sideChats")}
				className="cursor-chat-surface relative flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-background [font-size:14px]"
			>
				{tabBar}
				{closeDialog}
				{warning ? <div role="alert" className="shrink-0 border-b border-border bg-surface px-4 py-3 text-xs text-warning">{t(warning === "open" ? "sideChat.closeFailedOpen" : warning === "cleanup_pending" ? "sideChat.closeCleanupPending" : "sideChat.closeUnconfirmed")}</div> : null}
				{snapshot && snapshot.side.id === activeId ? (
					<>
						{!readOnly && snapshot.side.state !== "ready" ? (
							<p className="border-b border-border px-4 py-2 text-xs text-muted-foreground" role="status">
								{snapshot.side.errorMessage ||
									(snapshot.side.state === "recovering" ? t("sideChat.reconnecting") : snapshot.side.state)}
							</p>
						) : null}
						{conversation ? (
							<div className="relative flex min-h-0 flex-1 flex-col">
								{!readOnly && conversation.items.length === 0 ? (
									// The Timeline draws nothing for an empty transcript.
									<div className="mx-auto flex max-w-sm flex-1 flex-col items-center justify-center gap-2 px-4 py-8 text-center text-sm">
										<h3 className="font-medium">{t("sideChat.askASideQuestion")}</h3>
										<p className="text-xs leading-relaxed text-muted-foreground">{t("sideChat.emptyDescription")}</p>
									</div>
								) : null}
								<Timeline
									sideChat
									key={activeId}
									snapshot={conversation}
									annotationNavigationRef={timelineNavigationRef}
									draftScope={timelineDraftScope!}
									draftPersistenceAvailable={false}
									hasOlder={!readOnly && conversation.hasMoreBefore}
									loadingOlder={loadingOlder}
									onLoadOlder={() => void loadOlder()}
									excerptsEnabled={!readOnly}
									newWorkDisabled={readOnly}
									onAddToChat={(excerpt) => addReference(activeId, excerpt)}
									onForeignAnnotation={(annotation) => onNavigate?.(annotation)}
									onDecide={readOnly ? undefined : (requestId, decisionId) => void resolveApproval(activeId, requestId, decisionId)}
									busy={!readOnly && conversation.controller.state === "busy"}
									retryControl={readOnly ? undefined : { retry: (turnId) => retry(activeId, turnId) }}
								/>
							</div>
						) : null}
						{!readOnly && pendingInteractions.length ? (
							<div className="shrink-0 px-4 pb-2">
								<div className="mx-auto flex w-full max-w-3xl flex-col gap-2">
									{pendingInteractions.map(renderPendingInteraction)}
								</div>
							</div>
						) : null}
						{!readOnly && snapshot.side.state === "failed" && snapshot.side.recreateRequired ? (
							<Button
								type="button"
								variant="secondary"
								size="sm"
								onClick={() => void create(undefined, true).catch(() => undefined)}
								disabled={!canCreate || pending}
							>
								{t("sideChat.newSideChat")}
							</Button>
						) : !readOnly && snapshot.side.state === "failed" ? (
							<Button
								type="button"
								variant="secondary"
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
						{!readOnly && (drafts[activeId]?.pendingDelivery || drafts[activeId]?.pendingCommand) && !sendingRef.current.has(activeId) ? (
							<div className="px-3 text-xs text-muted-foreground">
								<p>{t("sideChat.deliveryIsUnconfirmedRetryTheOriginalQuestionBeforeSendingAnother")}</p>
								<Button
									type="button"
									size="sm"
									variant="ghost"
									disabled={sending}
									onClick={() => void send(activeId, "")}
								>
									{drafts[activeId]?.pendingCommand?.cancelRequested ? t("sideChat.finishCancellation") : t("sideChat.retryDelivery")}
								</Button>
								{drafts[activeId]?.pendingCommand ? <>
									<p>{t("sideChat.cancelHandoffHelp")}</p>
									{!drafts[activeId]?.pendingCommand?.cancelRequested ? <Button type="button" size="sm" variant="ghost" disabled={sending} onClick={() => void cancelHandoff(activeId)}>{t("sideChat.cancelHandoff")}</Button> : null}
								</> : null}
							</div>
						) : null}
						{!readOnly ? <SideComposer
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
							deliveryPending={Boolean(drafts[activeId]?.pendingDelivery || drafts[activeId]?.pendingCommand)}
							skills={skills}
							focusKey={focusKey}
							onFocusSide={canCreate ? () => void create(undefined, true).catch(() => undefined) : undefined}
							onRemoveReference={(id) =>
								setDrafts((current) => ({
									...current,
									[activeId]: {
										...(current[activeId] ?? emptySideChatDraft()),
										references: (current[activeId]?.references ?? []).filter((ref) => ref.id !== id),
									},
								}))
							}
							onRemoveAllReferences={() =>
								setDrafts((current) => ({
									...current,
									[activeId]: {
										...(current[activeId] ?? emptySideChatDraft()),
										references: [],
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
						/> : null}
					</>
				) : readOnly ? <div className="min-h-0 flex-1" /> : (
					<div
						className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center"
						role="status"
					>
						{activeError ? (
							<MessageSquare aria-hidden="true" className="size-5 text-muted-foreground" />
						) : (
							<Loader2 aria-hidden="true" className="size-5 animate-spin text-muted-foreground" />
						)}
						<p className="text-sm font-medium">
							{activeError ? t("sideChat.sideChatIsUnavailable") : t("sideChat.openingSideChat")}
						</p>
						<p className="max-w-xs text-xs leading-relaxed text-muted-foreground">
							{activeError
								? t(
										"sideChat.yourDraftStaysInThisThreadTryLoadingItAgainOrReturnToTheMainChatWhileTheConnectionRetriesAutomatically",
									)
								: t("sideChat.preparingThisSeparateThreadYouCanKeepWorkingInTheMainChat")}
						</p>
						{activeError ? (
							<Button
								type="button"
								variant="secondary"
								size="sm"
								onClick={() => {
									setError(undefined);
									setSideError(undefined);
									setSnapshotRefreshKey((key) => key + 1);
								}}
							>
								{t("sideChat.retryLoading")}
							</Button>
						) : null}
					</div>
				)}
				{activeError ? (
					<div className="shrink-0 border-t border-border bg-surface px-4 py-3 text-xs" role="alert">
						<p className="break-words text-destructive">{translateDraft(activeError!)}</p>
					</div>
				) : null}
			</aside>
		) : enabled && currentActiveId ? null : (
			<div className="flex h-full min-h-0 flex-col bg-background">
				{tabBar}
				{closeDialog}
				<div className="flex flex-1 flex-col items-center justify-center gap-3 p-4 text-center text-xs text-muted-foreground">
					<p>
						{enabled ? t("sideChat.noneOpen") : t("sideChat.sideChatsAreAvailableOnlyForLocalDesktopChatSessions")}
					</p>
					{activeError ? <p role="alert">{translateDraft(activeError)}</p> : null}
				</div>
			</div>
		);

	return {
		create,
		panel,
		pending,
		error: activeError,
		send,
		cancelHandoff,
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
