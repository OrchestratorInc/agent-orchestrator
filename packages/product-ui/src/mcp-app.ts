/** MCP Apps host protocol, independent of the browser or native view transport. */
export type MCPAppSnapshot = {
	server: string; tool: string; arguments: unknown; result?: unknown;
	live: boolean; ready: boolean; error?: string;
	csp?: Record<string, string[]>;
	policy?: string;
	permissions?: Record<string, unknown>;
};
export type MCPAppAction = { method: string; params: Record<string, unknown>; approvalToken?: string };
export type MCPAppActionResponse = { result?: unknown; approvalToken?: string; approvalTitle?: string };
export type MCPAppMode = "inline" | "fullscreen";
export type MCPAppHostContext = {
	theme: "light" | "dark";
	styles: { variables: Record<string, string> };
	displayMode: MCPAppMode;
	availableDisplayModes: MCPAppMode[];
	containerDimensions: { width: number; height?: number; maxHeight?: number };
	platform: "web" | "desktop" | "mobile";
	locale?: string;
	timeZone?: string;
};
export type MCPAppDownload = { name: string; mimeType: string; text?: string; blob?: string; uri?: string };

export function mcpAppPath(conversation: string, activity: string): string {
	return `/api/v1/conversations/${encodeURIComponent(conversation)}/mcp-apps/${encodeURIComponent(activity)}`;
}

export function mcpAppAllowAttribute(permissions: Record<string, unknown> | undefined): string {
	return Object.entries({ camera: "camera", microphone: "microphone", geolocation: "geolocation", clipboardWrite: "clipboard-write" })
		.flatMap(([key, feature]) => object(permissions?.[key]) ? [feature] : []).join("; ");
}

/** No credential-bearing URL reaches a remote app; the meta CSP only tightens its iframe sandbox. */
export function mcpAppSandboxDocument(html: string, policy: string): string {
	const escaped = policy.replace(/^sandbox[^;]*;/, "").replaceAll("&", "&amp;").replaceAll('"', "&quot;").replaceAll("<", "&lt;");
	const meta = `<meta http-equiv="Content-Security-Policy" content="${escaped}">`;
	const doctype = /^\uFEFF?\s*<!doctype[^>]*>/i.exec(html);
	return doctype ? html.slice(0, doctype[0].length) + meta + html.slice(doctype[0].length) : meta + html;
}

function object(value: unknown): value is Record<string, unknown> {
	return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** Codex uses null for absent optional fields; the MCP SDK requires omission. */
export function mcpAppToolResult(value: unknown): Record<string, unknown> {
	if (!object(value)) return { content: [] };
	const result: Record<string, unknown> = { ...value, content: Array.isArray(value.content) ? value.content : [] };
	for (const key of ["_meta", "structuredContent"]) if (result[key] == null) delete result[key];
	return result;
}

export function mcpAppDownloads(value: unknown): MCPAppDownload[] {
	if (!Array.isArray(value) || !value.length || value.length > 5) throw new Error("Choose between one and five files.");
	let bytes = 0;
	const files = value.map((entry): MCPAppDownload => {
		if (!object(entry)) throw new Error("Invalid file.");
		const resource = entry.type === "resource" && object(entry.resource) ? entry.resource : entry;
		const uri = typeof resource.uri === "string" ? resource.uri : "";
		const name = (typeof resource.name === "string" ? resource.name : uri.split(/[/?#]/).filter(Boolean).at(-1) ?? "download")
			.replace(/[\\/:*?"<>|\x00-\x1f\x7f]/g, "_").replace(/^\.+/, "_").slice(0, 160) || "download";
		const mimeType = typeof resource.mimeType === "string" ? resource.mimeType : "application/octet-stream";
		if (entry.type === "resource_link") {
			if (!uri || uri.length > 4096) throw new Error("A download resource URI is required.");
			return { name, mimeType, uri };
		}
		if (entry.type !== "resource") throw new Error("Invalid file resource.");
		if (typeof resource.text === "string") {
			bytes += new TextEncoder().encode(resource.text).length;
			return { name, mimeType, text: resource.text };
		}
		if (typeof resource.blob !== "string" || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(resource.blob)) throw new Error("Invalid base64 file.");
		bytes += resource.blob.length * 3 / 4;
		return { name, mimeType, blob: resource.blob };
	});
	if (bytes > 25 * 1024 * 1024) throw new Error("Files exceed 25 MiB.");
	return files;
}

export function mcpAppWebURL(value: unknown): value is string {
	if (typeof value !== "string" || value.length > 4096) return false;
	try { const url = new URL(value); return (url.protocol === "https:" || url.protocol === "http:") && !url.username && !url.password; } catch { return false; }
}

export function mcpAppStyleVariables(colors: Record<string, string>): Record<string, string> {
	const mapping: Record<string, string> = {
		"--color-background-primary": "--background", "--color-background-secondary": "--card",
		"--color-background-tertiary": "--muted", "--color-background-info": "--accent",
		"--color-text-primary": "--foreground", "--color-text-secondary": "--muted-foreground",
		"--color-text-tertiary": "--muted-foreground", "--color-text-info": "--primary",
		"--color-border-primary": "--border", "--color-border-secondary": "--border",
		"--color-text-danger": "--destructive", "--color-ring-primary": "--ring",
	};
	return { "--font-sans": "system-ui, sans-serif", "--font-mono": "ui-monospace, monospace", "--font-weight-normal": "400", "--font-weight-medium": "500", "--font-weight-semibold": "600", "--border-radius-sm": "4px", "--border-radius-md": "8px", "--border-radius-lg": "12px", ...Object.fromEntries(Object.entries(mapping).flatMap(([key, source]) => colors[source] ? [[key, colors[source]]] : [])) };
}

export function createMCPAppHost(options: {
	post(message: unknown): void;
	context(): MCPAppHostContext;
	action(request: MCPAppAction): Promise<MCPAppActionResponse>;
	confirm(title: string, detail: string): Promise<boolean>;
	openLink(url: string): Promise<void>;
	download(files: MCPAppDownload[]): Promise<void>;
	displayMode(mode: MCPAppMode): Promise<MCPAppMode>;
	height(height: number): void;
	ready(): void;
	close(): void;
}) {
	let initialized = false;
	let initializing = false;
	let disposed = false;
	let call: MCPAppSnapshot | undefined;
	let inputSent = false;
	let resultSent = false;
	let appModes: unknown;
	let teardownResolve: (() => void) | undefined;
	let teardownPromise: Promise<void> | undefined;
	const pending = new Set<string | number>();
	const send = (message: unknown) => { if (!disposed) options.post(message); };
	const notify = (method: string, params: unknown) => send({ jsonrpc: "2.0", method, params });
	const sendCall = () => {
		if (!initialized || !call) return;
		if (!inputSent) { inputSent = true; notify("ui/notifications/tool-input", { arguments: call.arguments ?? {} }); }
		if (!resultSent && call.result != null) { resultSent = true; notify("ui/notifications/tool-result", mcpAppToolResult(call.result)); }
	};
	const action = async (method: string, params: Record<string, unknown>) => {
		const request = { method, params };
		let response = await options.action(request);
		if (response.approvalToken) {
			if (disposed || !await options.confirm(response.approvalTitle ?? "Allow app action?", JSON.stringify(params, null, 2))) throw new Error("Action declined.");
			if (disposed) throw new Error("App closed.");
			response = await options.action({ ...request, approvalToken: response.approvalToken });
		}
		return method === "tools/call" ? mcpAppToolResult(response.result) : response.result ?? {};
	};
	const request = async (method: string, params: Record<string, unknown>): Promise<unknown> => {
		if (method === "ui/initialize") {
			if (initializing) throw new Error("App already initialized.");
			initializing = true;
			appModes = object(params.appCapabilities) ? params.appCapabilities.availableDisplayModes : undefined;
			return { protocolVersion: "2026-01-26", hostInfo: { name: "Agent Orchestrator", version: "1.0.0" },
				hostCapabilities: { serverTools: {}, serverResources: {}, openLinks: {}, downloadFile: {}, logging: {}, message: { text: {} }, updateModelContext: { text: {}, structuredContent: {} }, sandbox: { csp: call?.csp ?? {}, permissions: call?.permissions ?? {} } },
				hostContext: options.context() };
		}
		if (method === "ping") return {};
		if (!initialized) throw new Error("Initialize the app before requesting actions.");
		if (teardownPromise && method !== "ui/update-model-context") throw new Error("App is closing.");
		switch (method) {
			case "tools/call":
				if (typeof params.name !== "string" || !params.name.trim() || (params.arguments != null && !object(params.arguments))) throw new Error("A tool name and object arguments are required.");
				return action(method, { name: params.name, arguments: params.arguments ?? {} });
			case "resources/read":
			case "ui/update-model-context": return action(method, params);
			case "ui/message": return action(method, { ...params, content: Array.isArray(params.content) ? params.content : [params.content] });
			case "ui/open-link":
				if (!mcpAppWebURL(params.url)) throw new Error("Only HTTP and HTTPS links are supported.");
				if (!await options.confirm("Open link?", params.url) || disposed) throw new Error("Link declined.");
				await options.openLink(params.url); return {};
			case "ui/download-file": {
				const files = mcpAppDownloads(params.contents);
				if (!await options.confirm("Save files from this app?", files.map(f => f.name + (f.uri ? `\n${f.uri}` : "")).join("\n")) || disposed) return { isError: true };
				// Resource links belong to the originating MCP server. They are not
				// navigation targets and never become native filesystem paths.
				const resolved: unknown[] = [];
				for (const file of files) {
					if (file.uri) {
						const response = await action("resources/read", { uri: file.uri });
						const contents = object(response) && Array.isArray(response.contents) ? response.contents : [];
						const resource = contents.find(r => object(r) && r.uri === file.uri) ?? contents[0];
						if (!object(resource)) throw new Error(`No contents for ${file.name}.`);
						resolved.push({ type: "resource", resource: { ...resource, name: file.name } });
					} else resolved.push({ type: "resource", resource: file });
				}
				await options.download(mcpAppDownloads(resolved)); return {};
			}
			case "ui/request-display-mode": {
				const mode = params.mode;
				if ((mode !== "inline" && mode !== "fullscreen") || (Array.isArray(appModes) && !appModes.includes(mode))) return { mode: options.context().displayMode };
				return { mode: await options.displayMode(mode) };
			}
			default: throw new Error(`Unsupported app method: ${method}`);
		}
	};
	return {
		receive(data: unknown) {
			if (disposed || !object(data) || data.jsonrpc !== "2.0") return;
			if (data.id === "ao-teardown" && ("result" in data || "error" in data)) { teardownResolve?.(); return; }
			const id = typeof data.id === "string" || typeof data.id === "number" ? data.id : undefined;
			const fail = (message: string) => { if (id !== undefined) send({ jsonrpc: "2.0", id, error: { code: -32000, message } }); };
			let size: number;
			try { size = JSON.stringify(data).length; } catch { return; }
			if (size > (data.method === "ui/download-file" ? 36 * 1024 * 1024 : 256 * 1024)) { fail("App message is too large."); return; }
			if (typeof data.method !== "string") return;
			const params = object(data.params) ? data.params : {};
			if (id === undefined) {
				if (data.method === "ui/notifications/initialized" && initializing && !initialized) { initialized = true; options.ready(); sendCall(); }
				if (!initialized) return;
				if (data.method === "ui/notifications/size-changed" && typeof params.height === "number" && Number.isFinite(params.height)) options.height(Math.max(100, Math.min(2000, params.height)));
				if (data.method === "ui/notifications/request-teardown") options.close();
				return;
			}
			if (pending.has(id)) return;
			if (pending.size >= 16) { fail("Too many pending app requests."); return; }
			pending.add(id);
			void request(data.method, params).then(result => send({ jsonrpc: "2.0", id, result }), error => fail(error instanceof Error ? error.message : "App request failed.")).finally(() => pending.delete(id));
		},
		setCall(next: MCPAppSnapshot) { call = next; sendCall(); },
		updateContext() { if (initialized) notify("ui/notifications/host-context-changed", options.context()); },
		teardown(): Promise<void> {
			if (teardownPromise) return teardownPromise;
			if (disposed || !initialized) { disposed = true; return Promise.resolve(); }
			teardownPromise = new Promise(resolve => {
				const timer = setTimeout(() => teardownResolve?.(), 2000);
				teardownResolve = () => { clearTimeout(timer); disposed = true; resolve(); };
				send({ jsonrpc: "2.0", id: "ao-teardown", method: "ui/resource-teardown", params: {} });
			});
			return teardownPromise;
		},
	};
}
