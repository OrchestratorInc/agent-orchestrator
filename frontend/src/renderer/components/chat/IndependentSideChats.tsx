import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { components } from "../../../api/schema";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import type { ChatModel } from "../../types/conversation";
import { apiClient, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { ChatMarkdown } from "./ChatMarkdown";

type Side = components["schemas"]["SideConversation"];
type Snapshot = components["schemas"]["SideSnapshot"];

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
	const [editingTurnId, setEditingTurnId] = useState<string>();
	const [editingText, setEditingText] = useState("");
	const [inputAnswers, setInputAnswers] = useState<Record<string, Record<string, string>>>({});
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const sessionRef = useRef(sessionId);
	sessionRef.current = sessionId;
	useEffect(() => {
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
		if (!activeId) { setSnapshot(undefined); return; }
		let alive = true;
		const refresh = async () => {
			const { data, error: requestError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
				params: { path: { sessionId, sideId: activeId } },
			});
			if (!alive) return;
			if (requestError) { setError(apiErrorMessage(requestError)); return; }
			if (data?.snapshot) setSnapshot(data.snapshot);
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
		if (!activeId || !snapshot) return;
		const oldest = olderPages.at(-1)?.turns?.[0]?.createdAt ?? snapshot.turns?.[0]?.createdAt;
		if (!oldest) return;
		const { data, error: requestError } = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
			params: { path: { sessionId, sideId: activeId }, query: { before: oldest, limit: 50 } },
		});
		if (requestError) { setError(apiErrorMessage(requestError)); return; }
		if (data?.snapshot) setOlderPages((current) => [...current, data.snapshot]);
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
			setSides((current) => [...current.filter((side) => side.id !== data.side.id), data.side]);
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
		const timer = window.setTimeout(() => {
			void apiClient.PUT("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
				params: { path: { sessionId, sideId: activeId } }, body: { contentJson: drafts[activeId] },
			});
		}, 350);
		return () => window.clearTimeout(timer);
	}, [sessionId, activeId, drafts]);

	const send = useCallback(async (sideId: string, text: string, extraAttachments: { mimeType: string; data: string }[] = []) => {
		setError(undefined);
		const files = attachments[sideId] ?? [];
		let paths: string[] = [];
		let imagePayloads: { mimeType: string; data: string }[] = [];
		try {
			imagePayloads = [...extraAttachments, ...await Promise.all(files.map(sideFilePayload))];
			paths = await stageAttachments(imagePayloads);
		} catch (cause) { setError(apiErrorMessage(cause)); return; }
		const deliveredText = paths.length ? `${text.trim()}\n\nAttached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`.trim() : text;
		const { error: requestError } = await apiClient.POST("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/messages", {
			params: { path: { sessionId, sideId } }, body: { text: deliveredText, clientMessageId: crypto.randomUUID(),
				attachments: nativeImages ? imagePayloads : [] },
		});
		if (requestError) { setError(apiErrorMessage(requestError)); return; }
		await aoBridge.sideChats.capture().catch(() => undefined);
		updateDraft(sideId, "");
		setAttachments((current) => ({ ...current, [sideId]: [] }));
	}, [sessionId, updateDraft, attachments, stageAttachments, nativeImages]);

	const close = useCallback(async (sideId: string) => {
		const { error: requestError } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", {
			params: { path: { sessionId, sideId } },
		});
		if (requestError) { setError(apiErrorMessage(requestError)); return; }
		await aoBridge.sideChats.capture().catch(() => undefined);
		setDrafts((current) => { const next = { ...current }; delete next[sideId]; return next; });
		setAttachments((current) => { const next = { ...current }; delete next[sideId]; return next; });
		setSides((current) => current.filter((side) => side.id !== sideId));
		if (activeId === sideId) { setActiveId(undefined); setSnapshot(undefined); }
		setVisible(false);
	}, [sessionId, activeId]);

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
		<aside aria-label="Side chats" className="flex h-full min-h-0 w-[min(42vw,520px)] shrink-0 flex-col border-l border-border bg-background">
			<div className="border-b border-border px-3 py-2 text-xs font-medium">/btw</div>
			{snapshot && snapshot.side.id === activeId ? <>
				<div className="border-b border-border px-3 py-2 text-xs text-muted-foreground">
					{snapshot.side.contextMode === "native" ? "Native chat context through the selected turn" :
						snapshot.side.contextMode === "reconstructed" ? "Recorded visible chat context through the selected turn" : "Fresh side chat"} · current workspace files
					{snapshot.side.selectedText ? <details className="mt-1"><summary>Selected text</summary>
						<blockquote className="whitespace-pre-wrap border-l-2 pl-2">{snapshot.side.selectedText}</blockquote></details> : null}
					{snapshot.side.state !== "ready" ? <p role="status">{snapshot.side.errorMessage ?? snapshot.side.state}</p> : null}
					{models.length ? <div className="mt-2 flex gap-2">
						<label>Model <select aria-label="Side chat model" value={snapshot.side.model ?? ""}
							onChange={(event) => void updateSettings(activeId, event.target.value, snapshot.side.effort ?? "")}>
							{models.map((model) => <option key={model.id} value={model.id}>{model.displayName}</option>)}
						</select></label>
						<label>Effort <select aria-label="Side chat effort" value={snapshot.side.effort || models.find((model) => model.id === snapshot.side.model)?.defaultEffort || ""}
							onChange={(event) => void updateSettings(activeId, snapshot.side.model ?? "", event.target.value)}>
							{(models.find((model) => model.id === snapshot.side.model)?.efforts ?? []).map((effort) =>
								<option key={effort} value={effort}>{effort}</option>)}</select></label>
					</div> : null}
				</div>
				<div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
					{(olderPages.at(-1)?.hasMore ?? snapshot.hasMore) ? <button type="button" className="text-xs underline" onClick={() => void loadOlder()}>Load earlier messages</button> : null}
					{timeline.map((item) => item.kind === "message" ? <div key={item.message.id} className="rounded border border-border p-2 text-sm">
						<div className="mb-1 text-xs text-muted-foreground">{item.message.role}</div>
						<ChatMarkdown text={item.message.text} streaming={item.message.streaming} />
					</div> : (() => { const activity = item.activity; return <div key={activity.id} className="rounded border border-border p-2 text-xs">
						<p className="font-medium">{activity.summary || activity.kind}</p>
						{activity.text ? <pre className="max-h-48 overflow-auto whitespace-pre-wrap">{activity.text}</pre> : null}
						{activity.kind === "approval" && activity.status === "pending" && activity.requestId ?
							<div className="flex flex-wrap gap-2 pt-2">{activity.decisions?.map((decision) =>
								<button key={decision.id} type="button" className="rounded border border-border px-2 py-1"
									onClick={() => void resolveApproval(activeId, activity.requestId!, decision.id)}>{decision.label || decision.id}</button>)}</div> : null}
						{activity.kind === "input" && activity.status === "pending" && activity.requestId && activity.input ?
							<div className="space-y-2 pt-2">
								<p>{activity.input.message}</p>
								{activity.input.url && /^https?:\/\//i.test(activity.input.url) ? <a className="underline" href={activity.input.url} target="_blank" rel="noreferrer">Open requested URL</a> : null}
								{Object.keys((activity.input.schema?.properties ?? {}) as Record<string, unknown>).map((key) =>
									<label key={key} className="block">{key}<input className="ml-2 rounded border border-border bg-background p-1"
										value={inputAnswers[activity.requestId!]?.[key] ?? ""}
										onChange={(event) => setInputAnswers((current) => ({ ...current, [activity.requestId!]: { ...current[activity.requestId!], [key]: event.target.value } }))} /></label>)}
								<div className="flex gap-2"><button type="button" onClick={() => void resolveInput(activeId, activity.requestId!, "accept")}>Submit</button>
									<button type="button" onClick={() => void resolveInput(activeId, activity.requestId!, "decline")}>Decline</button></div>
							</div> : null}
					</div>; })())}
					{(snapshot.turns ?? []).filter((turn) => turn.state === "queued" || turn.state === "failed").map((turn) => <div key={turn.id} className="text-xs">
						{turn.state === "failed" ? <><p role="alert" className="text-destructive">{turn.errorMessage}</p>
							<button type="button" onClick={() => void retry(activeId, turn.id)}>Retry question</button></> :
							<><span>Queued</span> <button type="button" onClick={() => { setEditingTurnId(turn.id); setEditingText(turn.text); }}>Edit</button></>}
						{editingTurnId === turn.id ? <div><textarea aria-label="Edit queued side question" value={editingText}
							onChange={(event) => setEditingText(event.target.value)} />
							<button type="button" onClick={() => void saveQueuedEdit(activeId)}>Save</button>
							<button type="button" onClick={() => setEditingTurnId(undefined)}>Cancel</button></div> : null}
					</div>)}
				</div>
				<form className="border-t border-border p-3" onSubmit={(event) => {
					event.preventDefault(); const text = drafts[activeId] ?? ""; if (text.trim() || attachments[activeId]?.length) void send(activeId, text);
				}}>
					{attachments[activeId]?.length ? <div className="mb-2 flex flex-wrap gap-1 text-xs">{attachments[activeId].map((file, index) =>
						<button key={`${file.name}-${index}`} type="button" className="rounded border border-border px-2 py-1"
							onClick={() => setAttachments((current) => ({ ...current, [activeId]: current[activeId].filter((_, i) => i !== index) }))}>
							{file.name} ×</button>)}</div> : null}
					<textarea aria-label="Side chat question" className="w-full rounded border border-border bg-background p-2 text-sm"
						value={drafts[activeId] ?? ""} onChange={(event) => updateDraft(activeId, event.target.value)}
						placeholder="Message the side chat" />
					<div className="flex justify-end gap-2 text-xs">
						<label className="cursor-pointer rounded border border-border px-2 py-1">Attach image
							<input className="sr-only" type="file" accept="image/*" multiple onChange={(event) => {
								const files = Array.from(event.target.files ?? []);
								setAttachments((current) => ({ ...current, [activeId]: [...(current[activeId] ?? []), ...files] }));
								event.target.value = "";
							}} /></label>
						<button type="button" onClick={() => void interrupt(activeId)}>Stop</button>
						<button type="submit" disabled={snapshot.side.state !== "ready" || (!drafts[activeId]?.trim() && !attachments[activeId]?.length)}>Send</button>
					</div>
				</form>
			</> : <p className="p-3 text-xs text-muted-foreground">Opening side chat…</p>}
			{error ? <p className="p-2 text-xs text-destructive" role="alert">{error}</p> : null}
		</aside>
	) : null;

	return { create, panel, pending, error, send, setQuestionDraft: updateDraft,
		activeId: currentActiveId, visible, show: () => setVisible(true), hide: () => setVisible(false), close };
}
