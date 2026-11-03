import { randomBytes, randomUUID } from "node:crypto";
import { WebSocket, WebSocketServer, type RawData } from "ws";

const MAX_CDP_COMMAND_BYTES = 1 << 20;

export type AgentBrowserDebugger = {
	attach(protocolVersion?: string): void;
	detach(): void;
	isAttached(): boolean;
	sendCommand(method: string, commandParams?: Record<string, unknown>, sessionId?: string): Promise<unknown>;
	on(event: "message" | "detach", listener: (...args: unknown[]) => void): unknown;
	off?(event: "message" | "detach", listener: (...args: unknown[]) => void): unknown;
};

export type AgentBrowserTarget = {
	id: string;
	url: string;
	title: string;
	debugger: AgentBrowserDebugger;
};

export type AgentBrowserTargetProvider = {
	listTargets(): AgentBrowserTarget[];
	createTarget(url: string): Promise<AgentBrowserTarget>;
	activateTarget(targetId: string): Promise<void> | void;
	closeTarget(targetId: string): Promise<void> | void;
};

type CDPRequest = {
	id: number;
	method: string;
	params?: Record<string, unknown>;
	sessionId?: string;
};

type ConnectionContext = {
	socket: WebSocket;
	discoveredTargets: Map<string, TargetMetadata> | null;
};

type TargetMetadata = Pick<AgentBrowserTarget, "title" | "url"> & { targetId: string };

type AttachedTarget = {
	targetId: string;
	connection: ConnectionContext;
	protocolSessionId?: string;
	chromiumChildSessionIds: Set<string>;
};

/**
 * One physical Electron debugger attachment can serve multiple logical CDP
 * clients from the restricted agent-browser daemon. Detaching one logical
 * client must not detach the underlying page debugger while another remains.
 */
type PhysicalTargetAttachment = {
	targetId: string;
	debugger: AgentBrowserDebugger;
	clients: Map<string, AttachedTarget>;
	messageListener: (...args: unknown[]) => void;
	detachListener: (...args: unknown[]) => void;
	ownedDebugger: boolean;
};

export class AgentBrowserCDPBridge {
	private readonly server: WebSocketServer;
	private readonly pathToken = randomBytes(32).toString("base64url");
	private readonly attached = new Map<string, AttachedTarget>();
	private readonly physical = new Map<string, PhysicalTargetAttachment>();
	private readonly connections = new Set<ConnectionContext>();
	private endpoint = "";

	constructor(private readonly targets: AgentBrowserTargetProvider) {
		this.server = new WebSocketServer({
			host: "127.0.0.1",
			port: 0,
			path: `/${this.pathToken}`,
			maxPayload: MAX_CDP_COMMAND_BYTES,
		});
	}

	async start(): Promise<string> {
		if (this.endpoint) return this.endpoint;
		await new Promise<void>((resolve, reject) => {
			this.server.once("listening", resolve);
			this.server.once("error", reject);
		});
		const address = this.server.address();
		if (!address || typeof address === "string") {
			throw new Error("Unable to determine agent-browser CDP bridge address");
		}
		this.endpoint = `ws://127.0.0.1:${address.port}/${this.pathToken}`;
		this.server.on("connection", (socket) => {
			this.handleConnection(socket, { socket, discoveredTargets: null });
		});
		return this.endpoint;
	}

	async close(): Promise<void> {
		for (const sessionId of [...this.attached.keys()]) this.detachTarget(sessionId);
		for (const socket of this.server.clients) socket.close(1001, "AO browser session closed");
		await new Promise<void>((resolve) => this.server.close(() => resolve()));
		this.physical.clear();
		this.connections.clear();
		this.endpoint = "";
	}

	reconcileTargets(): void {
		const targets = this.listTargets();
		const currentIds = new Set(targets.map((target) => target.id));
		for (const [sessionId, attached] of this.attached) {
			if (!currentIds.has(attached.targetId)) this.detachTarget(sessionId);
		}
		for (const connection of this.connections) this.reconcileConnection(connection, targets);
	}

	private reconcileConnection(connection: ConnectionContext, targets: AgentBrowserTarget[]): void {
		const known = connection.discoveredTargets;
		if (!known) return;
		for (const target of targets) {
			const current = { targetId: target.id, title: target.title, url: target.url || "about:blank" };
			const previous = known.get(target.id);
			if (!previous || previous.title !== current.title || previous.url !== current.url) {
				known.set(target.id, current);
				this.send(connection.socket, {
					method: previous ? "Target.targetInfoChanged" : "Target.targetCreated",
					params: { targetInfo: this.targetInfo(target) },
				});
			}
		}
		const currentIds = new Set(targets.map((target) => target.id));
		for (const targetId of known.keys()) {
			if (currentIds.has(targetId)) continue;
			known.delete(targetId);
			this.send(connection.socket, { method: "Target.targetDestroyed", params: { targetId } });
		}
	}

	private handleConnection(socket: WebSocket, connection: ConnectionContext): void {
		this.connections.add(connection);
		socket.on("message", (data) => {
			// CDP correlates responses by request id and permits them to complete out
			// of order. Do not serialize independent Page/DOM/Runtime/Network requests.
			void this.handleMessage(connection, data).catch(() => undefined);
		});
		socket.on("close", () => {
			this.connections.delete(connection);
			for (const [sessionId, attached] of this.attached) {
				if (attached.connection.socket === socket) this.detachTarget(sessionId);
			}
		});
	}

	private async handleMessage(connection: ConnectionContext, raw: RawData): Promise<void> {
		let request: CDPRequest;
		try {
			request = JSON.parse(raw.toString()) as CDPRequest;
		} catch {
			return;
		}
		if (!Number.isInteger(request.id) || typeof request.method !== "string") return;
		try {
			// Browser-level target management stays on the unscoped connection;
			// page commands require a session created by Target.attachToTarget.
			const result = request.sessionId
				? await this.forwardTargetCommand(connection, request)
				: await this.handleBrowserCommand(connection, request);
			this.send(connection.socket, {
				id: request.id,
				result: result ?? {},
				...(request.sessionId ? { sessionId: request.sessionId } : {}),
			});
		} catch (error) {
			this.send(connection.socket, {
				id: request.id,
				error: {
					code: -32000,
					message: error instanceof Error ? error.message : "CDP command failed",
				},
				...(request.sessionId ? { sessionId: request.sessionId } : {}),
			});
		}
	}

	private async handleBrowserCommand(connection: ConnectionContext, request: CDPRequest): Promise<unknown> {
		const params = request.params ?? {};
		switch (request.method) {
			case "Browser.getVersion":
				return {
					protocolVersion: "1.3",
					product: `Chrome/${process.versions.chrome ?? "0"}`,
					revision: "",
					userAgent: "AO agent-browser bridge",
					jsVersion: process.versions.v8 ?? "",
				};
			case "Target.setDiscoverTargets": {
				if (params.discover === true) {
					connection.discoveredTargets ??= new Map();
					this.reconcileConnection(connection, this.listTargets());
				} else {
					connection.discoveredTargets = null;
				}
				return {};
			}
			case "Target.setAutoAttach": {
				return {};
			}
			case "Target.getTargets":
				return { targetInfos: this.listTargets().map((target) => this.targetInfo(target)) };
			case "Target.getTargetInfo": {
				const target = this.requireTarget(optionalString(params.targetId) ?? this.listTargets()[0]?.id);
				return { targetInfo: this.targetInfo(target) };
			}
			case "Target.attachToTarget": {
				const target = this.requireTarget(optionalString(params.targetId));
				const sessionId = this.attachTarget(connection, target);
				this.send(connection.socket, {
					method: "Target.attachedToTarget",
					params: {
						sessionId,
						targetInfo: this.targetInfo(target),
						waitingForDebugger: false,
					},
				});
				return { sessionId };
			}
			case "Target.detachFromTarget": {
				const sessionId = optionalString(params.sessionId);
				if (sessionId) {
					const attached = this.attached.get(sessionId);
					if (attached && attached.connection.socket !== connection.socket) {
						throw new Error("Target session belongs to another CDP client");
					}
					this.detachTarget(sessionId);
				}
				return {};
			}
			case "Target.sendMessageToTarget": {
				const sessionId = optionalString(params.sessionId);
				const encoded = optionalString(params.message);
				if (!sessionId || !encoded) throw new Error("Target.sendMessageToTarget requires sessionId and message");
				let nested: CDPRequest;
				try {
					nested = JSON.parse(encoded) as CDPRequest;
				} catch {
					throw new Error("Target.sendMessageToTarget message must be valid JSON");
				}
				if (typeof nested.method !== "string") throw new Error("Target message method is required");
				return this.forwardTargetCommand(connection, { ...nested, sessionId });
			}
			case "Target.createTarget": {
				const url = safeNavigationURL(optionalString(params.url) ?? "about:blank");
				const target = await this.targets.createTarget(url);
				this.reconcileTargets();
				return { targetId: target.id };
			}
			case "Target.activateTarget": {
				const target = this.requireTarget(optionalString(params.targetId));
				await this.targets.activateTarget(target.id);
				return {};
			}
			case "Target.closeTarget": {
				const target = this.requireTarget(optionalString(params.targetId));
				for (const [sessionId, attached] of this.attached) {
					if (attached.targetId === target.id) this.detachTarget(sessionId, false);
				}
				await this.targets.closeTarget(target.id);
				this.reconcileTargets();
				return { success: true };
			}
			case "Browser.getWindowForTarget":
				return { windowId: 1, bounds: { left: 0, top: 0, width: 1280, height: 720, windowState: "normal" } };
			case "Schema.getDomains":
				return { domains: [] };
			case "Browser.close":
				throw new Error("Browser.close is not permitted for AO-owned previews");
			default:
				throw new Error(`Unsupported browser-level CDP method: ${request.method}`);
		}
	}

	private attachTarget(connection: ConnectionContext, target: AgentBrowserTarget): string {
		const sessionId = `ao-${randomUUID()}`;
		const physical = this.ensurePhysical(target);
		const client: AttachedTarget = {
			targetId: target.id,
			connection,
			protocolSessionId: sessionId,
			chromiumChildSessionIds: new Set(),
		};
		physical.clients.set(sessionId, client);
		this.attached.set(sessionId, client);
		return sessionId;
	}

	private async forwardTargetCommand(connection: ConnectionContext, request: CDPRequest): Promise<unknown> {
		const requestedSessionId = request.sessionId!;
		let attached = this.attached.get(requestedSessionId);
		let chromiumSessionId: string | undefined;
		if (attached && attached.connection.socket !== connection.socket) {
			throw new Error("Target session belongs to another CDP client");
		}
		if (!attached) {
			attached = [...this.attached.values()].find(
				(candidate) =>
					candidate.connection.socket === connection.socket &&
					candidate.chromiumChildSessionIds.has(requestedSessionId),
			);
			chromiumSessionId = attached ? requestedSessionId : undefined;
		}
		if (!attached) throw new Error("Unknown or expired target session");
		const target = this.requireTarget(attached.targetId);
		assertSafeTargetMethod(request.method, request.params);
		return chromiumSessionId
			? target.debugger.sendCommand(request.method, request.params, chromiumSessionId)
			: target.debugger.sendCommand(request.method, request.params);
	}

	private ensurePhysical(target: AgentBrowserTarget): PhysicalTargetAttachment {
		const existing = this.physical.get(target.id);
		if (existing) return existing;
		const ownedDebugger = !target.debugger.isAttached();
		if (ownedDebugger) target.debugger.attach("1.3");
		const physical: PhysicalTargetAttachment = {
			targetId: target.id,
			debugger: target.debugger,
			clients: new Map(),
			messageListener: () => undefined,
			detachListener: () => undefined,
			ownedDebugger,
		};
		physical.messageListener = (...args: unknown[]) => {
			const method = typeof args[1] === "string" ? args[1] : "";
			if (!method) return;
			const params = isRecord(args[2]) ? args[2] : {};
			const chromiumSessionId = optionalString(args[3]);
			for (const client of physical.clients.values()) {
				const eventSessionId = client.protocolSessionId
					? chromiumSessionId && client.chromiumChildSessionIds.has(chromiumSessionId)
						? chromiumSessionId
						: client.protocolSessionId
					: chromiumSessionId;
				this.send(client.connection.socket, {
					method,
					params,
					...(eventSessionId ? { sessionId: eventSessionId } : {}),
				});
				const childSessionId = optionalString(params.sessionId);
				if (method === "Target.attachedToTarget" && childSessionId) {
					client.chromiumChildSessionIds.add(childSessionId);
				} else if (method === "Target.detachedFromTarget" && childSessionId) {
					client.chromiumChildSessionIds.delete(childSessionId);
				}
			}
		};
		physical.detachListener = () => {
			for (const client of physical.clients.values()) {
				this.send(client.connection.socket, {
					method: "Inspector.detached",
					params: { reason: "AO page debugger was released" },
					...(client.protocolSessionId ? { sessionId: client.protocolSessionId } : {}),
				});
				if (client.protocolSessionId) this.attached.delete(client.protocolSessionId);
				client.connection.socket.close(1012, "AO page debugger was released");
			}
			physical.clients.clear();
			this.physical.delete(target.id);
		};
		target.debugger.on("message", physical.messageListener);
		target.debugger.on("detach", physical.detachListener);
		this.physical.set(target.id, physical);
		return physical;
	}

	private listTargets(): AgentBrowserTarget[] {
		return this.targets.listTargets();
	}

	private requireTarget(targetId: string | undefined): AgentBrowserTarget {
		if (!targetId) throw new Error("targetId is required");
		const target = this.listTargets().find((candidate) => candidate.id === targetId);
		if (!target) throw new Error("Target is outside this AO worker");
		return target;
	}

	private targetInfo(target: AgentBrowserTarget): Record<string, unknown> {
		return {
			targetId: target.id,
			type: "page",
			title: target.title,
			url: target.url || "about:blank",
			attached: this.physical.has(target.id),
			canAccessOpener: false,
		};
	}

	private detachTarget(sessionId: string, detachDebugger = true): void {
		const attached = this.attached.get(sessionId);
		if (!attached) return;
		this.attached.delete(sessionId);
		const physical = this.physical.get(attached.targetId);
		physical?.clients.delete(sessionId);
		if (physical) this.detachPhysicalIfUnused(physical, detachDebugger);
	}

	private detachPhysicalIfUnused(physical: PhysicalTargetAttachment, detachDebugger: boolean): void {
		if (physical.clients.size > 0) return;
		this.physical.delete(physical.targetId);
		physical.debugger.off?.("message", physical.messageListener);
		physical.debugger.off?.("detach", physical.detachListener);
		if (detachDebugger && physical.ownedDebugger && physical.debugger.isAttached()) {
			try {
				physical.debugger.detach();
			} catch {
				// Electron may already be tearing the WebContents down.
			}
		}
	}

	private send(socket: WebSocket, message: unknown): void {
		if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message));
	}
}

function assertSafeTargetMethod(method: string, params: Record<string, unknown> | undefined): void {
	if (method === "Page.navigate") safeNavigationURL(optionalString(params?.url) ?? "");
	if (
		method === "Browser.close" ||
		method === "Browser.setDownloadBehavior" ||
		method === "Page.close" ||
		method === "Page.setDownloadBehavior" ||
		method === "Page.printToPDF" ||
		method === "DOM.setFileInputFiles" ||
		method.startsWith("Fetch.") ||
		method.startsWith("Storage.") ||
		method === "Network.getAllCookies" ||
		method === "Network.getCookies" ||
		method === "Network.setCookie" ||
		method === "Network.setCookies" ||
		method === "Network.clearBrowserCookies"
	) {
		throw new Error(`CDP method is not permitted by AO: ${method}`);
	}
}

function safeNavigationURL(raw: string): string {
	if (raw === "about:blank") return raw;
	let url: URL;
	try {
		url = new URL(raw);
	} catch {
		throw new Error("Navigation requires an explicit HTTP(S) URL");
	}
	if (url.protocol !== "http:" && url.protocol !== "https:") {
		throw new Error(`Navigation scheme is not permitted: ${url.protocol}`);
	}
	return url.href;
}

function optionalString(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return Boolean(value && typeof value === "object" && !Array.isArray(value));
}
