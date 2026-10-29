import { createMCPAppHost, mcpAppAllowAttribute, mcpAppPath, mcpAppSandboxDocument, mcpAppStyleVariables, type MCPAppHostContext, type MCPAppSnapshot } from "@aoagents/product-ui";
import createClient from "openapi-fetch";
import { Loader2, Maximize2, Minimize2, RotateCcw, X } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { paths } from "../../../api/schema";
import { apiErrorMessage } from "../../lib/api-client";
import { Button } from "../ui/button";
import { useChatApiBaseUrl, useChatRemoteHost } from "./chat-image-source";
import { useRenderTheme } from "./RenderFrame";

type Consent = { title: string; detail: string; resolve(approved: boolean): void };

/** The same frame stays mounted when the dialog enters/leaves the top layer. */
export function MCPAppFrame({ conversationId, activityId, completed }: { conversationId: string; activityId: string; completed: boolean }) {
	const baseUrl = useChatApiBaseUrl();
	const remote = useChatRemoteHost();
	const [remoteHTML, setRemoteHTML] = useState<string>();
	const client = useMemo(() => createClient<paths>({ baseUrl }), [baseUrl]);
	const path = mcpAppPath(conversationId, activityId);
	const [snapshot, setSnapshot] = useState<MCPAppSnapshot>();
	const [failure, setFailure] = useState<string>();
	const [attempt, setAttempt] = useState(0);
	const [ready, setReady] = useState(false);
	const [closed, setClosed] = useState(false);
	const [expanded, setExpanded] = useState(false);
	const [height, setHeight] = useState(360);
	const [consent, setConsent] = useState<Consent>();
	const consentRef = useRef<Consent | undefined>(undefined);
	const dialog = useRef<HTMLDialogElement>(null);
	const frame = useRef<HTMLIFrameElement>(null);
	const host = useRef<ReturnType<typeof createMCPAppHost> | undefined>(undefined);
	const mode = useRef(expanded);
	mode.current = expanded;
	const theme = useRenderTheme();
	const currentTheme = useRef(theme);
	currentTheme.current = theme;
	const snapshotRef = useRef(snapshot);
	snapshotRef.current = snapshot;
	const confirm = (title: string, detail: string): Promise<boolean> => new Promise(resolve => {
		// A frame cannot replace a request the user is currently considering.
		if (consentRef.current) { resolve(false); return; }
		const next = { title, detail, resolve };
		consentRef.current = next;
		setConsent(next);
	});
	const settleConsent = (approved: boolean) => {
		consentRef.current?.resolve(approved);
		consentRef.current = undefined;
		setConsent(undefined);
	};
	useEffect(() => {
		if (!baseUrl) { setFailure("Reconnect to the host to load this app."); return; }
		const abort = new AbortController();
		const timeout = setTimeout(() => { setFailure("Loading the app timed out. Retry when the host is connected."); abort.abort(); }, 30_000);
		setFailure(undefined);
		void client.GET("/api/v1/conversations/{conversationId}/mcp-apps/{activityId}", { params: { path: { conversationId, activityId } }, signal: abort.signal }).then(async ({ data, error }) => {
			if (abort.signal.aborted) return;
			if (error || !data) throw new Error(apiErrorMessage(error, "Could not load app."));
			setSnapshot(data);
			if (data.error) setFailure(data.error);
			if (remote && data.ready) {
				// The proxy base URL contains a bearer capability. Fetch in the host
				// and frame a document with no proxy URL, credentials or referrer.
				const document = await client.GET("/api/v1/conversations/{conversationId}/mcp-apps/{activityId}/document", { params: { path: { conversationId, activityId } }, signal: abort.signal, parseAs: "text" });
				if (document.error || !document.data || !data.policy) throw new Error("Could not load the remote app document.");
				if (!abort.signal.aborted) setRemoteHTML(mcpAppSandboxDocument(document.data, data.policy));
			}
		}).catch(error => { if (!abort.signal.aborted) setFailure(String(error instanceof Error ? error.message : error)); }).finally(() => clearTimeout(timeout));
		return () => { clearTimeout(timeout); abort.abort(); };
	}, [client, baseUrl, conversationId, activityId, completed, attempt, remote]);
	useLayoutEffect(() => {
		const element = dialog.current;
		if (!element) return;
		// close/showModal changes only the dialog's top-layer status, not its DOM.
		element.close();
		if (expanded) element.showModal(); else element.show();
		host.current?.updateContext();
	}, [expanded, closed]);
	useLayoutEffect(() => {
		if (!snapshot?.ready || closed || !baseUrl || (remote && !remoteHTML)) return;
		const abort = new AbortController();
		setReady(false);
		const context = (): MCPAppHostContext => ({
			theme: currentTheme.current.appearance,
			styles: { variables: mcpAppStyleVariables(currentTheme.current.variables) },
			displayMode: mode.current ? "fullscreen" : "inline", availableDisplayModes: ["inline", "fullscreen"],
			containerDimensions: { width: frame.current?.clientWidth ?? 600, ...(mode.current ? { height: frame.current?.clientHeight ?? 600 } : { maxHeight: 2000 }) },
			platform: window.ao ? "desktop" : "web", locale: navigator.language, timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
		});
		let timer: ReturnType<typeof setTimeout>;
		const bridge = createMCPAppHost({
			post: message => frame.current?.contentWindow?.postMessage(message, "*"), context, confirm,
			action: async request => {
				const { data, error } = await client.POST("/api/v1/conversations/{conversationId}/mcp-apps/{activityId}/actions", { params: { path: { conversationId, activityId } }, body: request, signal: abort.signal });
				if (error || !data) { const message = apiErrorMessage(error, "App action failed."); setFailure(message); throw new Error(message); }
				return data;
			},
			openLink: async url => { window.open(url, "_blank", "noopener,noreferrer"); },
			download: async files => {
				for (const file of files) {
					if (file.uri) { window.open(file.uri, "_blank", "noopener,noreferrer"); continue; }
					const bytes = file.blob === undefined ? file.text ?? "" : Uint8Array.from(atob(file.blob), c => c.charCodeAt(0));
					const url = URL.createObjectURL(new Blob([bytes], { type: file.mimeType }));
					const link = document.createElement("a"); link.href = url; link.download = file.name; link.click();
					setTimeout(() => URL.revokeObjectURL(url), 30_000);
				}
			},
			displayMode: async next => {
				if (next === "fullscreen" && !mode.current && !await confirm("Expand app?", "This app wants to use the full window.")) return "inline";
				setExpanded(next === "fullscreen"); return next;
			},
			height: next => { if (!mode.current) setHeight(next); }, ready: () => { clearTimeout(timer); setReady(true); setFailure(undefined); }, close: () => { void bridge.teardown().then(() => setClosed(true)); },
		});
		host.current = bridge;
		if (snapshotRef.current) bridge.setCall(snapshotRef.current);
		const receive = (event: MessageEvent) => { if (event.source === frame.current?.contentWindow && event.origin === "null") bridge.receive(event.data); };
		window.addEventListener("message", receive);
		const observer = new ResizeObserver(() => bridge.updateContext());
		if (frame.current) observer.observe(frame.current);
		timer = setTimeout(() => { if (!abort.signal.aborted) setFailure("This app has not connected. You can retry or inspect the tool result below."); }, 20_000);
		return () => {
			clearTimeout(timer); observer.disconnect(); window.removeEventListener("message", receive);
			consentRef.current?.resolve(false); consentRef.current = undefined; setConsent(undefined);
			abort.abort(); void bridge.teardown(); host.current = undefined;
		};
	}, [snapshot?.ready, closed, attempt, client, baseUrl, conversationId, activityId, remote, remoteHTML]);
	useEffect(() => { if (snapshot) host.current?.setCall(snapshot); }, [snapshot]);
	useEffect(() => host.current?.updateContext(), [theme]);
	useEffect(() => { if (ready) setFailure(undefined); }, [ready]);
	const retry = async () => { settleConsent(false); await host.current?.teardown(); setClosed(false); setReady(false); setFailure(undefined); setAttempt(n => n + 1); };
	return <dialog ref={dialog} open aria-label={`${snapshot?.server ?? "MCP"} app`} aria-modal={expanded} role={expanded ? "dialog" : "region"}
		onCancel={event => { event.preventDefault(); setExpanded(false); }}
		className={`m-0 overflow-hidden rounded-lg border border-border bg-background p-0 text-foreground backdrop:bg-black/50 ${expanded ? "fixed inset-4 m-auto flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col" : "relative w-full max-w-full"}`}>
		<div className="flex items-center gap-2 border-b border-border px-3 py-2 text-xs">
			<span className="min-w-0 flex-1 truncate font-medium">{snapshot?.server ?? "Interactive tool"}{snapshot && !snapshot.live ? " · Saved view" : ""}</span>
			<Button variant="ghost" size="icon" className="size-6" aria-label="Reload app" onClick={retry}><RotateCcw className="size-3.5" /></Button>
			<Button variant="ghost" size="icon" className="size-6" aria-label={expanded ? "Collapse app" : "Expand app"} onClick={() => setExpanded(v => !v)}>{expanded ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}</Button>
			{expanded && <Button variant="ghost" size="icon" className="size-6" aria-label="Close expanded view" onClick={() => setExpanded(false)}><X className="size-3.5" /></Button>}
		</div>
		{consent && <div className="border-b border-border bg-muted p-3 text-sm" role="alertdialog" aria-label={consent.title}>
			<p className="font-medium">{consent.title}</p><pre className="my-2 max-h-48 overflow-auto whitespace-pre-wrap break-words text-xs">{consent.detail}</pre>
			<div className="flex gap-2"><Button size="sm" variant="outline" onClick={() => settleConsent(false)}>Decline</Button><Button size="sm" onClick={() => settleConsent(true)}>Allow once</Button></div>
		</div>}
		{(failure || closed) && <div className="flex items-center gap-2 p-3 text-xs text-muted-foreground" role="status"><span className="flex-1">{closed ? "The app closed its view." : failure}</span><Button size="sm" variant="outline" onClick={retry}>Retry</Button></div>}
		{!ready && !failure && !closed && <div role="status" className="flex items-center gap-2 p-3 text-xs text-muted-foreground"><Loader2 className="size-3 animate-spin" />Loading app…</div>}
		{snapshot?.ready && !closed && (!remote || remoteHTML) && <iframe key={attempt} ref={frame} title={`${snapshot.server} interactive app`} src={remote ? undefined : `${baseUrl}${path}/document`} srcDoc={remote ? remoteHTML : undefined} sandbox="allow-scripts allow-forms" referrerPolicy="no-referrer" allow={mcpAppAllowAttribute(snapshot.permissions)}
			className={expanded ? "min-h-0 w-full flex-1 border-0" : "block w-full border-0"} style={expanded ? undefined : { height }} onError={() => setFailure("The app document could not load.")} />}
	</dialog>;
}
