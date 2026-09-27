import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { ArrowUp, Loader2, Plus, Square, X } from "lucide-react";
import type { components } from "../../../api/schema";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import type { ChatModel, ConversationActivity, ConversationMessage } from "../../types/conversation";
import { apiClient, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { ActivityRow, ApprovalCard, AssistantMessage, HumanMessage } from "./ChatTimelineItems";
import { ComposerEditor, type ComposerEditorHandle } from "./ComposerEditor";
import { TurnSettingsBar } from "./TurnSettingsBar";
import { QueuedMessageDock } from "./QueuedMessageDock";
import { Button } from "../ui/button";

type Side = components["schemas"]["SideConversation"];
type Snapshot = components["schemas"]["SideSnapshot"];

function SideComposer({ value, onChange, onSend, onInterrupt, onAttach, files, onRemoveFile,
	ready, running, sending, models, side, onSettings, queuedDock }: {
	value: string;
	onChange: (text: string) => void;
	onSend: (text: string) => void;
	onInterrupt: () => void;
	onAttach: (files: File[]) => void;
	files: File[];
	onRemoveFile: (index: number) => void;
	ready: boolean;
	running: boolean;
	sending: boolean;
	models: ChatModel[];
	side: Side;
	onSettings: (model: string, effort: string) => void;
	queuedDock?: ReactNode;
}) {
	const editor = useRef<ComposerEditorHandle>(null);
	const filePicker = useRef<HTMLInputElement>(null);
	useEffect(() => {
		if (editor.current?.getSnapshot().text !== value) editor.current?.setText(value);
	}, [value]);
	const canSend = ready && !sending && Boolean(value.trim() || files.length);
	return <div className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
		<div aria-hidden="true" className="chat-composer-fade" />
		<div className="mx-auto w-full max-w-3xl">
			{queuedDock}
		<form className="cursor-chat-composer relative mx-auto flex w-full max-w-3xl cursor-text flex-col gap-1.5 border px-3 pt-3 pb-3"
			onSubmit={(event) => { event.preventDefault(); if (canSend) onSend(editor.current?.getSnapshot().text ?? value); }}
			onClick={(event) => { if (event.target === event.currentTarget) editor.current?.focus(); }}>
			{files.length ? <ul className="flex flex-wrap gap-1.5" aria-label="Attached files">{files.map((file, index) =>
				<li key={`${file.name}-${index}`} className="flex items-center gap-1.5 rounded border border-border bg-background px-2 py-1 text-[11px]">
					<span className="max-w-[120px] truncate" title={file.name}>{file.name}</span>
					<button type="button" aria-label={`Remove ${file.name}`} onClick={() => onRemoveFile(index)}><X className="size-3" /></button>
				</li>)}</ul> : null}
			<ComposerEditor ref={editor} disabled={!ready || sending} label="Side chat question"
				placeholder={ready ? running ? "Agent is working — this sends when it finishes" : "Message the agent…" : "The controller is not connected"}
				menuOpen={false} menuId="side-chat-completions" activeIndex={0}
				onChange={(next) => onChange(next.text)} onComplete={() => undefined}
				onEnter={(next, event) => { if (event.shiftKey) return false; if (ready && !sending && (next.text.trim() || files.length)) onSend(next.text); return true; }}
				onCompositionChange={() => {}} onKeyDown={() => {}} onPaste={() => {}} />
			<div className="flex h-7 items-center gap-1.5">
				<div role="group" aria-label="Message tools" className="flex min-w-0 flex-1 items-center gap-0.5">
					<input ref={filePicker} type="file" multiple hidden onChange={(event) => {
						onAttach(Array.from(event.target.files ?? [])); event.target.value = "";
					}} />
					<Button type="button" variant="ghost" size="icon-sm" disabled={!ready || sending}
						aria-label="Attach a file" onClick={() => filePicker.current?.click()}
						className="size-7 shrink-0 rounded-full p-0 text-muted-foreground hover:bg-white/5! hover:text-foreground">
						<Plus aria-hidden="true" className="size-3.5" />
					</Button>
					<TurnSettingsBar models={models} settings={{ model: side.model, reasoningEffort: side.effort }}
						onChange={(next) => onSettings(next.model ?? "", next.reasoningEffort ?? "")}
						hideApproval disabled={!ready || sending} />
				</div>
				<Button type={running && !canSend ? "button" : "submit"} variant="ghost" size="icon-sm"
					aria-label={running && !canSend ? "Stop turn" : "Send message"}
					onClick={running && !canSend ? onInterrupt : undefined}
					disabled={running && !canSend ? false : !canSend}
					className="size-7 rounded-full bg-foreground text-background hover:bg-foreground/90 hover:text-background">
					{running && !canSend ? <Square aria-hidden="true" className="size-2.5 fill-current" /> :
						sending ? <Loader2 aria-hidden="true" className="size-3.5 animate-spin" /> : <ArrowUp aria-hidden="true" className="size-3.5" />}
				</Button>
			</div>
		</form>
		</div>
	</div>;
}

async function sideFilePayload(file: File): Promise<{ mimeType: string; data: string }> {
	const dataUrl = await new Promise<string>((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result));
		reader.onerror = () => reject(reader.error);
		reader.readAsDataURL(file);
	});
	return { mimeType: file.type || "application/octet-stream", data: dataUrl.split(",", 2)[1] ?? "" };
}

export function useIndependentSideChats(sessionId: string, models: ChatModel[],
	stageAttachments: (items: { mimeType: string; data: string }[]) => Promise<string[]>, nativeImages: boolean) {
	const [sides, setSides] = useState<Side[]>([]);
	const [activeId, setActiveId] = useState<string>();
	const [visible, setVisible] = useState(false);
	const [snapshot, setSnapshot] = useState<Snapshot>();
	const [olderPages, setOlderPages] = useState<Snapshot[]>([]);
	const [drafts, setDrafts] = useState<Record<string, string>>({});
	const [attachments, setAttachments] = useState<Record<string, File[]>>({});
	const [error, setError] = useState<string>();
	const [pending, setPending] = useState(false);
	const [sending, setSending] = useState(false);
	const sendingRef = useRef(new Set<string>());
	const [editingTurnId, setEditingTurnId] = useState<string>();
	const [editingText, setEditingText] = useState("");
	const [inputAnswers, setInputAnswers] = useState<Record<string, Record<string, string>>>({});
	const activeIdRef = useRef(activeId);
	activeIdRef.current = activeId;
	const closedSidesRef = useRef(new Set<string>());
	const loadingOlderRef = useRef(false);
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const sessionRef = useRef(sessionId);
	sessionRef.current = sessionId;
	useEffect(() => {
		closedSidesRef.current.clear();
		setSides([]);
		setActiveId(undefined);
		setVisible(false);
		setSnapshot(undefined);
		setOlderPages([]);
		setDrafts({});
		setAttachments({});
		setError(undefined);
	}, [sessionId]);

	const refreshList = useCallback(async () => {
		const { data, error: requestError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats", {
			params: { path: { sessionId } },
		});
		if (sessionRef.current !== sessionId) return;
		if (requestError) { setError(apiErrorMessage(requestError)); return; }
		const nextSides = Array.isArray(data?.sides) ? data.sides : [];
		setSides(nextSides);
		setActiveId((current) => current && nextSides.some((side) => side.id === current)
			? current : nextSides.at(-1)?.id);
		if (nextSides.length === 0) setVisible(false);
	}, [sessionId]);

	useEffect(() => {
		void refreshList();
		const timer = window.setInterval(() => void refreshList(), 2000);
		return () => window.clearInterval(timer);
	}, [refreshList]);

	useEffect(() => {
		setOlderPages([]);
		loadingOlderRef.current = false;
		if (!activeId) { setSnapshot(undefined); return; }
		let alive = true;
		let lastApplied = 0;
		let issued = 0;
		const refresh = async () => {
			const requestNumber = ++issued;
			const { data, error: requestError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
				params: { path: { sessionId, sideId: activeId } },
			});
			if (!alive || requestNumber < lastApplied) return;
			lastApplied = requestNumber;
			if (requestError) { setError(apiErrorMessage(requestError)); return; }
			if (data?.snapshot) { setSnapshot(data.snapshot); setError(undefined); }
		};
		void refresh();
		let stream: EventSource | undefined;
		if (baseUrl && typeof EventSource !== "undefined") {
			stream = new EventSource(`${baseUrl}/api/v1/sessions/${encodeURIComponent(sessionId)}/conversation/side-chats/${encodeURIComponent(activeId)}/events`);
			stream.addEventListener("changed", () => void refresh());
			stream.addEventListener("snapshot", () => void refresh());
		}
		const timer = window.setInterval(() => void refresh(), stream ? 3000 : 500);
		return () => { alive = false; window.clearInterval(timer); stream?.close(); };
	}, [sessionId, activeId, baseUrl]);

	const loadOlder = useCallback(async () => {
		if (!activeId || !snapshot || loadingOlderRef.current) return;
		const oldest = olderPages.at(-1)?.turns?.[0]?.createdAt ?? snapshot.turns?.[0]?.createdAt;
		if (!oldest) return;
		loadingOlderRef.current = true;
		try {
			const { data, error: requestError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
				params: { path: { sessionId, sideId: activeId }, query: { before: oldest, limit: 50 } },
			});
			if (requestError) { setError(apiErrorMessage(requestError)); return; }
			if (data?.snapshot) setOlderPages((current) => [...current, data.snapshot]);
		} finally { loadingOlderRef.current = false; }
	}, [sessionId, activeId, snapshot, olderPages]);

	useEffect(() => {
		if (!activeId || drafts[activeId] !== undefined) return;
		void apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
			params: { path: { sessionId, sideId: activeId } },
		}).then(({ data }) => {
			if (sessionRef.current !== sessionId) return;
			if (data) setDrafts((current) => ({ ...current, [activeId]: current[activeId] ?? data.contentJson }));
		});
	}, [sessionId, activeId, drafts]);

	const create = useCallback(async (excerpt?: ChatDraftExcerptReference) => {
		setPending(true);
		setError(undefined);
		try {
			const { data, error: requestError } = await apiClient.POST("/api/v1/sessions/{sessionId}/conversation/side-chats", {
				params: { path: { sessionId } },
				body: { idempotencyKey: crypto.randomUUID(),
					label: excerpt ? excerpt.text.slice(0, 80) : undefined,
					reference: excerpt ? { conversationId: excerpt.conversationId, messageId: excerpt.messageId,
						revision: excerpt.revision, text: excerpt.text } : undefined },
			});
			if (requestError) throw requestError;
			await aoBridge.sideChats.capture().catch(() => undefined);
			setSides((current) => current.some((side) => side.id === data.side.id)
				? current.map((side) => side.id === data.side.id ? data.side : side)
				: [...current, data.side]);
			setActiveId(data.side.id);
			setVisible(true);
			return { id: data.side.id };
		} catch (cause) { setError(apiErrorMessage(cause)); throw cause; }
		finally { setPending(false); }
	}, [sessionId]);

	const updateDraft = useCallback((sideId: string, text: string) => {
		setDrafts((current) => ({ ...current, [sideId]: text }));
	}, []);

	useEffect(() => {
		if (!activeId || drafts[activeId] === undefined) return;
		const draft = drafts[activeId];
		const save = () => void apiClient.PUT("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
			params: { path: { sessionId, sideId: activeId } }, body: { contentJson: draft },
		});
		const timer = window.setTimeout(() => {
			save();
		}, 350);
		return () => {
			window.clearTimeout(timer);
			if (!closedSidesRef.current.has(activeId) && (activeIdRef.current !== activeId || sessionRef.current !== sessionId)) save();
		};
	}, [sessionId, activeId, drafts]);

	const send = useCallback(async (sideId: string, text: string, extraAttachments: { mimeType: string; data: string }[] = []) => {
		const btw = /^\/btw(?:\s+|$)/i.exec(text);
		if (btw) {
			text = text.slice(btw[0].length).trim();
			updateDraft(sideId, text);
			if (!text) return;
		}
		if (sendingRef.current.has(sideId)) return;
		sendingRef.current.add(sideId);
		setSending(true);
		setError(undefined);
		try {
			const files = attachments[sideId] ?? [];
			const imagePayloads = [...extraAttachments, ...await Promise.all(files.map(sideFilePayload))];
			const paths = await stageAttachments(imagePayloads);
			const deliveredText = paths.length ? `${text.trim()}\n\nAttached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`.trim() : text;
			const { error: requestError } = await apiClient.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/messages", {
				params: { path: { sessionId, sideId } }, body: { text: deliveredText, clientMessageId: crypto.randomUUID(),
					attachments: nativeImages ? imagePayloads : [] },
			});
			if (requestError) throw requestError;
			await aoBridge.sideChats.capture().catch(() => undefined);
			updateDraft(sideId, "");
			setAttachments((current) => ({ ...current, [sideId]: [] }));
		} catch (cause) { setError(apiErrorMessage(cause)); }
		finally {
			sendingRef.current.delete(sideId);
			setSending(sendingRef.current.size > 0);
		}
	}, [sessionId, updateDraft, attachments, stageAttachments, nativeImages]);

	const close = useCallback(async (sideId: string) => {
		const { error: requestError } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
			params: { path: { sessionId, sideId } },
		});
		if (requestError) { setError(apiErrorMessage(requestError)); return; }
		closedSidesRef.current.add(sideId);
		await aoBridge.sideChats.capture().catch(() => undefined);
		setDrafts((current) => { const next = { ...current }; delete next[sideId]; return next; });
		setAttachments((current) => { const next = { ...current }; delete next[sideId]; return next; });
		const remaining = sides.filter((side) => side.id !== sideId);
		setSides(remaining);
		if (activeId === sideId) { setActiveId(remaining.at(-1)?.id); setSnapshot(undefined); }
		if (remaining.length === 0) setVisible(false);
	}, [sessionId, activeId, sides]);

	const interrupt = useCallback(async (sideId: string) => {
		const { error: requestError } = await apiClient.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/interrupt", {
			params: { path: { sessionId, sideId } },
		});
		if (requestError) setError(apiErrorMessage(requestError));
	}, [sessionId]);
	const resolveApproval = useCallback(async (sideId: string, requestId: string, decisionId: string) => {
		const { error: requestError } = await apiClient.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/approvals/{requestId}/resolve", {
			params: { path: { sessionId, sideId, requestId } }, body: { decisionId },
		});
		if (requestError) setError(apiErrorMessage(requestError));
	}, [sessionId]);
	const resolveInput = useCallback(async (sideId: string, requestId: string, action: "accept" | "decline" | "cancel") => {
		const { error: requestError } = await apiClient.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/inputs/{requestId}/resolve", {
			params: { path: { sessionId, sideId, requestId } }, body: { action, content: action === "accept" ? inputAnswers[requestId] ?? {} : {} },
		});
		if (requestError) setError(apiErrorMessage(requestError));
	}, [sessionId, inputAnswers]);
	const updateSettings = useCallback(async (sideId: string, model: string, effort: string) => {
		const { error: requestError } = await apiClient.PATCH("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/settings", {
			params: { path: { sessionId, sideId } }, body: { model, effort },
		});
		if (requestError) setError(apiErrorMessage(requestError));
	}, [sessionId]);
	const retry = useCallback(async (sideId: string, turnId: string) => {
		const { error: requestError } = await apiClient.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/retry", {
			params: { path: { sessionId, sideId, turnId } },
		});
		if (requestError) setError(apiErrorMessage(requestError));
	}, [sessionId]);
	const saveQueuedEdit = useCallback(async (sideId: string) => {
		if (!editingTurnId || !editingText.trim()) return;
		const { error: requestError } = await apiClient.PATCH("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}", {
			params: { path: { sessionId, sideId, turnId: editingTurnId } }, body: { text: editingText, clientMessageId: "" },
		});
		if (requestError) { setError(apiErrorMessage(requestError)); return; }
		setEditingTurnId(undefined);
	}, [sessionId, editingTurnId, editingText]);
	const timeline = snapshot ? [
		...[...olderPages.slice().reverse().flatMap((page) => page.messages ?? []), ...(snapshot.messages ?? [])]
			.map((message) => ({ kind: "message" as const, time: message.createdAt, sequence: message.sequence, message })),
		...[...olderPages.slice().reverse().flatMap((page) => page.activities ?? []), ...(snapshot.activities ?? [])]
			.map((activity) => ({ kind: "activity" as const, time: activity.createdAt, sequence: 0, activity })),
	].sort((a, b) => a.time.localeCompare(b.time) || a.sequence - b.sequence) : [];

	const currentActiveId = sides.some((side) => side.id === activeId && side.sessionId === sessionId) ? activeId : undefined;
	const panel = currentActiveId && visible ? (
		<aside aria-label="Side chats" className="cursor-chat-surface flex h-full min-h-0 w-1/2 min-w-[360px] shrink-0 flex-col border-l border-border bg-background [font-size:14px]">
			<div className="flex h-10 shrink-0 items-center border-b border-border px-4 text-xs font-medium">/btw</div>
			{snapshot && snapshot.side.id === activeId ? <>
				<div className="border-b border-border px-4 py-2 text-[11px] text-muted-foreground">
					{snapshot.side.contextMode === "native" ? "Native chat context through the selected turn" :
						snapshot.side.contextMode === "reconstructed" ? "Recorded visible chat context through the selected turn" : "Fresh side chat"} · current workspace files
					{snapshot.side.selectedText ? <details className="mt-1"><summary className="cursor-pointer">Selected text</summary>
						<blockquote className="mt-1 whitespace-pre-wrap border-l-2 border-logo-accent pl-2 text-foreground">{snapshot.side.selectedText}</blockquote></details> : null}
					{snapshot.side.state !== "ready" ? <p role="status">{snapshot.side.errorMessage ?? snapshot.side.state}</p> : null}
				</div>
				<div className="cursor-chat-timeline min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-6">
					<div className="mx-auto flex w-full max-w-3xl flex-col gap-5">
						{(olderPages.at(-1)?.hasMore ?? snapshot.hasMore) ? <button type="button" className="self-center text-xs text-muted-foreground underline" onClick={() => void loadOlder()}>Load earlier messages</button> : null}
						{timeline.map((item) => item.kind === "message" ? (() => {
							const message: ConversationMessage = { kind: "message", id: item.message.id, turnId: item.message.turnId,
								sequence: item.message.sequence, revision: item.message.revision,
								role: item.message.role === "user" ? "user" : "assistant",
								origin: item.message.role === "user" ? "human" : "provider", text: item.message.text,
								streaming: item.message.streaming, createdAt: item.message.createdAt };
							return message.role === "user" ? <HumanMessage key={message.id} message={message} sessionId={sessionId} /> :
								<AssistantMessage key={message.id} message={message} showCopy={!message.streaming} />;
						})() : (() => { const activity = item.activity;
							const conversationActivity: ConversationActivity = { kind: "activity", id: activity.id, turnId: activity.turnId,
								sequence: 0, revision: 1, activityKind: activity.kind === "input" ? "user_input" :
									(["command", "file_change", "plan", "reasoning", "approval", "usage", "error", "system", "mcp_tool", "auto_review", "user_input"].includes(activity.kind)
										? activity.kind as ConversationActivity["activityKind"] : "system"),
								status: activity.status as ConversationActivity["status"], summary: activity.summary || activity.kind,
								detail: (activity.kind === "command" && activity.text
									? { ...((activity.detail && typeof activity.detail === "object" && !Array.isArray(activity.detail)) ? activity.detail : {}), output: activity.text }
									: activity.detail) as ConversationActivity["detail"], requestId: activity.requestId,
								decisions: activity.decisions?.map((decision) => ({ id: decision.id, label: decision.label,
									kind: decision.kind as NonNullable<ConversationActivity["decisions"]>[number]["kind"] })),
								createdAt: activity.createdAt };
							return <div key={activity.id}>
							{activity.kind === "approval" ? <ApprovalCard activity={conversationActivity}
								onDecide={(requestId, decisionId) => void resolveApproval(activeId, requestId, decisionId)} /> :
								activity.kind === "user_input" || activity.kind === "input" ? null : <ActivityRow activity={conversationActivity} />}
							{(activity.kind === "user_input" || activity.kind === "input") && activity.status === "pending" && activity.requestId && activity.input ?
								<div className="space-y-2 pt-2">
									<p>{activity.input.message}</p>
									{activity.input.url && /^https?:\/\//i.test(activity.input.url) ? <a className="underline" href={activity.input.url} target="_blank" rel="noreferrer">Open requested URL</a> : null}
									{Object.keys((activity.input.schema?.properties ?? {}) as Record<string, unknown>).map((key) =>
										<label key={key} className="block">{key}<input className="ml-2 rounded border border-border bg-background p-1"
											value={inputAnswers[activity.requestId!]?.[key] ?? ""}
											onChange={(event) => setInputAnswers((current) => ({ ...current, [activity.requestId!]: { ...current[activity.requestId!], [key]: event.target.value } }))} /></label>)}
									<div className="flex gap-2"><Button type="button" size="sm" onClick={() => void resolveInput(activeId, activity.requestId!, "accept")}>Submit</Button>
										<Button type="button" size="sm" variant="outline" onClick={() => void resolveInput(activeId, activity.requestId!, "decline")}>Decline</Button></div>
							</div> : null}
						</div>; })())}
						{(snapshot.turns ?? []).some((turn) => turn.state === "running") ?
							<div className="flex min-h-6 items-center gap-2 px-1 py-0.5" data-testid="side-live-turn-status">
								<Loader2 aria-hidden="true" className="size-3 shrink-0 animate-spin text-status-working" />
								<span role="status" aria-live="polite" className="text-xs font-medium text-muted-foreground">Working…</span>
							</div> : null}
						{(snapshot.turns ?? []).filter((turn) => turn.state === "failed").map((turn) => <div key={turn.id} className="rounded-lg border border-border px-3 py-2 text-xs">
							<p role="alert" className="text-destructive">{turn.errorMessage}</p>
							<Button type="button" size="sm" variant="ghost" onClick={() => void retry(activeId, turn.id)}>Retry question</Button>
						</div>)}
					</div>
				</div>
				<SideComposer value={drafts[activeId] ?? ""} onChange={(text) => updateDraft(activeId, text)}
					onSend={(text) => void send(activeId, text)}
					onInterrupt={() => void interrupt(activeId)}
					onAttach={(files) => setAttachments((current) => ({ ...current, [activeId]: [...(current[activeId] ?? []), ...files] }))}
					onRemoveFile={(index) => setAttachments((current) => ({ ...current, [activeId]: (current[activeId] ?? []).filter((_, i) => i !== index) }))}
					files={attachments[activeId] ?? []} ready={snapshot.side.state === "ready"}
					running={(snapshot.turns ?? []).some((turn) => turn.state === "running")}
					sending={sending} models={models} side={snapshot.side} onSettings={(model, effort) => void updateSettings(activeId, model, effort)}
					queuedDock={(snapshot.turns ?? []).some((turn) => turn.state === "queued") ? <>
						<QueuedMessageDock messages={(snapshot.turns ?? []).filter((turn) => turn.state === "queued").map((turn, index) => ({
							turnId: turn.id, message: { kind: "message", id: `queued-${turn.id}`, turnId: turn.id, sequence: index + 1,
								revision: 1, role: "user", origin: "human", text: turn.text, streaming: false, createdAt: turn.createdAt },
						}))} onBeginQueuedEdit={(turnId, text) => { setEditingTurnId(turnId); setEditingText(text); }} />
						{editingTurnId ? <div className="border-x border-border bg-surface px-3 py-2 text-xs">
							<p className="mb-1 text-muted-foreground">Editing queued message</p>
							<textarea aria-label="Edit queued side question" className="w-full rounded border border-border bg-background p-2" value={editingText}
								onChange={(event) => setEditingText(event.target.value)} />
							<div className="flex gap-2"><Button type="button" size="sm" onClick={() => void saveQueuedEdit(activeId)}>Save</Button>
								<Button type="button" size="sm" variant="ghost" onClick={() => setEditingTurnId(undefined)}>Cancel</Button></div>
						</div> : null}
					</> : null} />
			</> : <p className="p-4 text-xs text-muted-foreground">Opening side chat…</p>}
			{error ? <p className="px-4 pb-2 text-xs text-destructive" role="alert">{error}</p> : null}
		</aside>
	) : null;

	return { create, panel, pending, error, send, setQuestionDraft: updateDraft,
		sides, activeId: currentActiveId, visible, show: (sideId?: string) => {
			if (sideId) setActiveId(sideId);
			setVisible(true);
		}, hide: () => setVisible(false), close };
}
