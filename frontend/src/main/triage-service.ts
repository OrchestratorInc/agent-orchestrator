import { spawn, type ChildProcessWithoutNullStreams, type SpawnOptionsWithoutStdio } from "node:child_process";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { request as httpRequest } from "node:http";
import os from "node:os";
import path from "node:path";
import type { IpcMain } from "electron";
import { resolveDaemonLaunch } from "../shared/daemon-launch";
import type { DaemonStatus } from "../shared/daemon-status";
import {
	TRIAGE_CHANNELS,
	type TriageBridge,
	type TriageChoice,
	type TriageOrigin,
	type TriageReplyInput,
	type TriageRun,
	type TriageStartRequest,
	type TriageTerminalOutput,
} from "../shared/triage";

export const MAX_TRIAGE_COMMAND_BYTES = 1024 * 1024;
export const MAX_TRIAGE_FRAME_BYTES = 8 * 1024 * 1024;
const MAX_PENDING_REQUESTS = 64;
const MAX_BUFFERED_COMMAND_BYTES = 4 * MAX_TRIAGE_COMMAND_BYTES;
const MAX_DIAGNOSTIC_BYTES = 16 * 1024;

type Command = Partial<TriageReplyInput> & {
	type: string;
	runId?: string;
	sessionId?: string;
	start?: TriageStartRequest;
	origin?: TriageOrigin;
	choices?: TriageChoice[];
	text?: string;
	rows?: number;
	cols?: number;
};
type Response = {
	version: 1;
	id?: string;
	type: string;
	run?: TriageRun;
	launch?: SessionLaunch;
	runs?: TriageRun[];
	choices?: TriageChoice[];
	runId?: string;
	data?: string;
	sequence?: number;
	error?: string;
};
type SessionLaunch = {
	clientRequestId: string;
	prompt: string;
	attachments: Array<{ mimeType: string; data: string }>;
};
type DaemonRequest = (port: number, method: "GET" | "POST", endpoint: "/healthz" | "/api/v1/sessions", body?: Record<string, unknown>) => Promise<{ status: number; body: Record<string, unknown> }>;
type SpawnHelper = (command: string, args: string[], options: SpawnOptionsWithoutStdio) => ChildProcessWithoutNullStreams;
export type TriageServiceOptions = {
	dataDir: string;
	resolveBinary: () => string;
	context: () => { appVersion: string; buildMode: string; daemonStatus: DaemonStatus; daemonOutput?: string };
	env?: () => NodeJS.ProcessEnv;
	spawnProcess?: SpawnHelper;
	terminateProcess?: (child: ChildProcessWithoutNullStreams) => void;
	binaryExists?: (binary: string) => boolean;
	requestTimeoutMs?: number;
	shutdownTimeoutMs?: number;
	requestDaemon?: DaemonRequest;
};

// Direct loopback requests never consult a proxy, follow redirects, or use a
// host supplied by the renderer. A failed POST has an unknown outcome, so its
// persisted clientRequestId must be reused on the next attempt.
const requestDaemon: DaemonRequest = (port, method, endpoint, body) => new Promise((resolve, reject) => {
	const raw = body === undefined ? undefined : JSON.stringify(body);
	const request = httpRequest({ hostname: "127.0.0.1", port, method, path: endpoint, agent: false,
		headers: raw ? { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(raw), "X-AO-Attachment-Upload": "1" } : undefined,
	}, (response) => {
		const chunks: Buffer[] = [];
		let size = 0;
		response.on("data", (chunk: Buffer) => {
			size += chunk.length;
			if (size > 2 * 1024 * 1024) { response.destroy(new Error("The daemon response exceeded 2 MiB.")); return; }
			chunks.push(chunk);
		});
		response.on("error", reject);
		response.on("end", () => {
			try { const raw = Buffer.concat(chunks).toString("utf8"); resolve({ status: response.statusCode ?? 0, body: raw ? record(JSON.parse(raw)) : {} }); }
			catch { reject(new Error("The daemon returned an invalid response.")); }
		});
	});
	const deadline = setTimeout(() => request.destroy(new Error("The standalone session request timed out.")), 30_000);
	request.on("close", () => clearTimeout(deadline));
	request.on("error", reject);
	request.end(raw);
});

/** Resolve the app-owned binary without consulting shell or daemon overrides. */
export function resolveTriageBinary(input: {
	isPackaged: boolean;
	resourcesPath: string;
	appPath: string;
	homeDir: string;
	platform: NodeJS.Platform;
}): string {
	const launch = resolveDaemonLaunch({}, input.isPackaged, input.resourcesPath, input.appPath, input.homeDir, input.platform);
	if (!launch || !path.isAbsolute(launch.command)) throw new Error("The bundled support helper path is unavailable.");
	return launch.command;
}

/** Scrub retained startup output before it enters a report or fallback text. */
export function scrubTriageDiagnostics(input: string): string {
	return input
		.replace(/-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?(?:-----END [^-]*PRIVATE KEY-----|$)/g, "[redacted private key]")
		.replace(/(\b(?:authorization|proxy-authorization|cookie|set-cookie)\s*:\s*)[^\r\n]*/gi, "$1[redacted]")
		.replace(/\b(Bearer|Basic)\s+[^\s,;"']+/gi, "$1 [redacted]")
		.replace(/\b(?:sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,}|github_pat_[A-Za-z0-9_]{12,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)/g, "[redacted]")
		.replace(/((?:[\w-]*(?:token|password|passwd|secret|credential|api[_-]?key|authorization|cookie)[\w-]*)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)/gi, "$1[redacted]")
		.replace(/(--(?:token|password|secret|api-key)(?:=|\s+))\S+/gi, "$1[redacted]")
		.replace(/\b(?:https?|wss?):\/\/[^\s<>"']+/gi, (raw) => {
			try {
				const url = new URL(raw);
				url.username = "";
				url.password = "";
				url.search = "";
				url.hash = "";
				return url.toString();
			} catch { return "[redacted URL]"; }
		})
		.replace(/(?:\/Users\/|\/home\/|[A-Z]:\\Users\\)[^\s/\\"']+/gi, "[home]")
		.slice(-MAX_DIAGNOSTIC_BYTES);
}

function record(value: unknown): Record<string, unknown> {
	if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid support request.");
	return value as Record<string, unknown>;
}

function text(value: unknown, limit = 1024, required = false): string {
	if (value === undefined && !required) return "";
	if (typeof value !== "string" || value.length > limit || (required && !value.trim()) || value.includes("\0")) {
		throw new Error("Invalid support request text.");
	}
	return value;
}

function cachedSnapshot(value: unknown): Record<string, unknown> {
	const source = record(value);
	const json = JSON.stringify(source);
	if (Buffer.byteLength(json) > 64 * 1024) throw new Error("The cached diagnostic snapshot is too large.");
	return JSON.parse(json) as Record<string, unknown>;
}

function choices(value: unknown): TriageChoice[] {
	if (!Array.isArray(value) || value.length > 64) throw new Error("Invalid configured support agents.");
	const serialized = JSON.stringify(value);
	if (Buffer.byteLength(serialized) > MAX_TRIAGE_COMMAND_BYTES) throw new Error("Configured support agents exceed 1 MiB.");
	return value as TriageChoice[];
}

function isRun(value: unknown): value is TriageRun {
	if (!value || typeof value !== "object") return false;
	const run = value as Partial<TriageRun>;
	return typeof run.id === "string" && run.version === 1 && typeof run.state === "string"
		&& typeof run.activity === "string" && typeof run.createdAt === "string" && typeof run.updatedAt === "string"
		&& Array.isArray(run.messages) && !!run.request && typeof run.request === "object";
}

function parseResponse(line: string): Response {
	const value = record(JSON.parse(line));
	if (value.version !== 1 || typeof value.type !== "string" || (value.id !== undefined && typeof value.id !== "string")) {
		throw new Error("Invalid support helper protocol.");
	}
	switch (value.type) {
		case "run": if (!isRun(value.run)) throw new Error("Invalid support run."); break;
		case "session-launch": {
			if (!isRun(value.run) || value.run.execution !== "session") throw new Error("Invalid standalone investigation.");
			if (value.launch !== undefined) {
				const launch = record(value.launch);
				text(launch.clientRequestId, 128, true);
				text(launch.prompt, 16 * 1024, true);
				if (!Array.isArray(launch.attachments) || !launch.attachments.length || launch.attachments.length > 8) throw new Error("Invalid investigation attachments.");
			} else if (!value.run.sessionId) throw new Error("The investigation has no launch payload.");
			break;
		}
		case "runs": if (value.runs !== undefined && (!Array.isArray(value.runs) || !value.runs.every(isRun))) throw new Error("Invalid support runs."); break;
		case "catalog": if (value.choices !== undefined) value.choices = choices(value.choices); break;
		case "error": if (typeof value.error !== "string") throw new Error("Invalid support helper error."); break;
		case "terminal-output":
			if (typeof value.runId !== "string" || typeof value.data !== "string" ||
				Buffer.from(value.data, "base64").toString("base64") !== value.data ||
				!Number.isSafeInteger(value.sequence) || Number(value.sequence) < 0) throw new Error("Invalid terminal output.");
			break;
		case "ack": case "quit": break;
		default: throw new Error("Unknown support helper response.");
	}
	return value as Response;
}

function terminateHelper(child: ChildProcessWithoutNullStreams): void {
	try {
		if (process.platform !== "win32" && child.pid) process.kill(-child.pid, "SIGTERM");
		else child.kill("SIGTERM");
	} catch { /* It may already have exited. */ }
}

/** One lazy process per desktop app. Unsubscribing never cancels its work. */
export class TriageService implements Omit<TriageBridge, "subscribeTerminal"> {
	private child: ChildProcessWithoutNullStreams | null = null;
	private failure: Error | null = null;
	private closing = false;
	private shutdownPromise: Promise<void> | null = null;
	private output = Buffer.alloc(0);
	private stderr = "";
	private listeners = new Set<(run: TriageRun) => void>();
	private terminalListeners = new Set<(output: TriageTerminalOutput) => void>();
	private pending = new Map<string, { expected: string | readonly string[]; resolve: (response: Response) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> }>();
	private terminationTimer: ReturnType<typeof setTimeout> | undefined;
	private exited: Promise<void> = Promise.resolve();

	constructor(private readonly options: TriageServiceOptions) {}

	private diagnostics(): string {
		const context = this.options.context();
		return scrubTriageDiagnostics([
			`AO ${context.appVersion}, ${context.buildMode}, ${process.platform}/${process.arch}`,
			`Daemon: ${context.daemonStatus.state}${context.daemonStatus.code ? ` (${context.daemonStatus.code})` : ""}`,
			context.daemonStatus.message, context.daemonStatus.details, context.daemonOutput, this.stderr,
		].filter(Boolean).join("\n"));
	}

	private error(message: string): Error {
		return new Error(`${message}\nYou can copy these details into a GitHub report:\n${this.diagnostics()}`);
	}

	private fail(error: Error): void {
		this.failure ??= error;
		for (const request of this.pending.values()) { clearTimeout(request.timer); request.reject(this.failure); }
		this.pending.clear();
	}

	private endFailedHelper(message: string): void {
		this.fail(this.error(message));
		const child = this.child;
		if (!child) return;
		child.stdin.end();
		this.terminationTimer ??= setTimeout(() => (this.options.terminateProcess ?? terminateHelper)(child), 1000);
		this.terminationTimer.unref();
	}

	private ensureHelper(): ChildProcessWithoutNullStreams {
		if (this.failure) throw this.failure;
		if (this.child) return this.child;
		try {
			const binary = this.options.resolveBinary();
			if (!path.isAbsolute(binary) || !(this.options.binaryExists ?? existsSync)(binary)) throw new Error("missing binary");
			const child = (this.options.spawnProcess ?? spawn)(binary, ["support-host", "--data-dir", this.options.dataDir], {
				cwd: path.dirname(binary), shell: false, stdio: "pipe", detached: process.platform !== "win32", windowsHide: true,
				env: this.options.env?.() ?? process.env,
			});
			this.child = child;
			this.exited = new Promise((resolve) => {
				child.once("exit", () => {
					if (this.terminationTimer) clearTimeout(this.terminationTimer);
					if (!this.closing) (this.options.terminateProcess ?? terminateHelper)(child);
					this.child = null;
					this.fail(this.error("The support helper exited. Restart AO to try again."));
					resolve();
				});
			});
			child.once("error", () => this.endFailedHelper("The support helper could not start."));
			child.stdin.on("error", () => this.endFailedHelper("The support helper connection closed."));
			child.stderr.on("data", (data: Buffer | string) => { this.stderr = (this.stderr + data.toString()).slice(-MAX_DIAGNOSTIC_BYTES); });
			child.stdout.on("data", (data: Buffer | string) => this.onData(Buffer.isBuffer(data) ? data : Buffer.from(data)));
			return child;
		} catch {
			this.failure = this.error("The bundled support helper is unavailable. Restart AO or reinstall the desktop app.");
			throw this.failure;
		}
	}

	private onData(data: Buffer): void {
		if (this.failure) return;
		// Never concatenate an unbounded read chunk, even when it contains newlines.
		let offset = 0;
		while (offset < data.length) {
			const newline = data.indexOf(10, offset);
			const end = newline < 0 ? data.length : newline;
			if (this.output.length + end - offset > MAX_TRIAGE_FRAME_BYTES) {
				this.endFailedHelper("The support helper sent an oversized response."); return;
			}
			this.output = Buffer.concat([this.output, data.subarray(offset, end)]);
			if (newline < 0) return;
			const line = this.output.toString("utf8");
			this.output = Buffer.alloc(0);
			offset = newline + 1;
			let response: Response;
			try { response = parseResponse(line); }
			catch { this.endFailedHelper("The support helper sent an invalid response."); return; }

			if (!response.id) {
				if (response.type === "run" && response.run) {
					for (const listener of this.listeners) { try { listener(response.run); } catch { /* A view cannot stop another subscriber. */ } }
				} else if (response.type === "terminal-output" && response.runId && response.data !== undefined && response.sequence !== undefined) {
					const output = { runId: response.runId, data: response.data, sequence: response.sequence };
					for (const listener of this.terminalListeners) { try { listener(output); } catch { /* A terminal view cannot stop another subscriber. */ } }
				} else if (response.type === "error") {
					this.endFailedHelper("The support helper reported a protocol error."); return;
				}
				continue;
			}
			const request = this.pending.get(response.id);
			if (!request) continue; // A timed-out response must not be retried or cancel the run.
			this.pending.delete(response.id);
			clearTimeout(request.timer);
			if (response.type === "error") request.reject(new Error(scrubTriageDiagnostics(response.error ?? "Support request failed.")));
			else if (!(Array.isArray(request.expected) ? request.expected.includes(response.type) : response.type === request.expected)) {
				request.reject(this.error("The support helper returned the wrong response."));
				this.endFailedHelper("The support helper returned the wrong response."); return;
			} else request.resolve(response);
		}
	}

	private request(command: Command, expected: string | readonly string[], timeoutMs = this.options.requestTimeoutMs ?? 30_000, quitting = false): Promise<Response> {
		if (this.closing && !quitting) return Promise.reject(new Error("The support helper is shutting down."));
		const id = randomUUID();
		const frame = JSON.stringify({ ...command, id }) + "\n";
		if (Buffer.byteLength(frame) > MAX_TRIAGE_COMMAND_BYTES) return Promise.reject(new Error("The support request is too large."));
		if (this.pending.size >= MAX_PENDING_REQUESTS) return Promise.reject(new Error("Too many support requests are pending."));
		let child: ChildProcessWithoutNullStreams;
		try { child = this.ensureHelper(); } catch (error) { return Promise.reject(error); }
		if (child.stdin.writableLength + Buffer.byteLength(frame) > MAX_BUFFERED_COMMAND_BYTES) return Promise.reject(new Error("The support helper is busy. Try again shortly."));
		return new Promise((resolve, reject) => {
			const timer = setTimeout(() => {
				this.pending.delete(id);
				reject(new Error("The support request timed out. The investigation may still be running; reopen it to check its progress."));
			}, timeoutMs);
			this.pending.set(id, { expected, resolve, reject, timer });
			try { child.stdin.write(frame); }
			catch { this.endFailedHelper("The support helper connection closed."); }
		});
	}

	async catalog(): Promise<TriageChoice[]> { return (await this.request({ type: "catalog" }, "catalog")).choices ?? []; }
	async configure(value: TriageChoice[]): Promise<void> { await this.request({ type: "configure", choices: choices(value) }, "catalog"); }
	async list(): Promise<TriageRun[]> { return (await this.request({ type: "list" }, "runs")).runs ?? []; }
	async get(runId: string): Promise<TriageRun> { return (await this.request({ type: "get", runId: text(runId, 128, true) }, "run")).run!; }
	private origin(value: unknown, context = this.options.context()): TriageOrigin {
		const provided = value === undefined ? {} : record(value);
		// Already available state only; never refresh or start the daemon.
		const cachedMachine = provided.machine === undefined ? {} : record(provided.machine);
		return {
			...(provided.hostId === undefined ? {} : { hostId: text(provided.hostId, 255) }),
			...(provided.surface === undefined ? {} : { surface: text(provided.surface, 128) }),
			...(provided.sessionId === undefined ? {} : { sessionId: text(provided.sessionId, 255) }),
			appVersion: context.appVersion, buildMode: context.buildMode,
			daemonState: context.daemonStatus.state, daemonCode: context.daemonStatus.code,
			daemonOutput: scrubTriageDiagnostics([context.daemonStatus.message, context.daemonStatus.details, context.daemonOutput].filter(Boolean).join("\n")),
			machine: { platform: process.platform, arch: process.arch, release: os.release(),
				...(cachedMachine.app === undefined ? {} : { app: cachedSnapshot(cachedMachine.app) }),
				system: cachedMachine.system === undefined ? {
					cpuCount: os.cpus().length, totalmem: os.totalmem(), freemem: os.freemem(), load1: os.loadavg()[0],
					cpuMeasured: false, cpuPercent: null, measuredAt: new Date().toISOString(), source: "electron-main",
				} : cachedSnapshot(cachedMachine.system),
				fetchedAt: cachedMachine.fetchedAt === undefined ? new Date().toISOString() : text(cachedMachine.fetchedAt, 128),
			},
			...(provided.session === undefined ? {} : { session: cachedSnapshot(provided.session) }),
		};
	}
	async prepare(value: TriageOrigin): Promise<TriageRun> {
		return (await this.request({ type: "prepare", origin: this.origin(value) }, "run")).run!;
	}
	async start(value: TriageStartRequest): Promise<TriageRun> {
		const source = record(value);
		if (typeof source.publishAuthorized !== "boolean") throw new Error("Choose whether to authorize publishing this report.");
		const context = this.options.context();
		// The bundled helper validates selection and preparation fields. Main owns
		// only desktop context and the daemon boundary.
		const start: TriageStartRequest = { ...value, origin: this.origin(value.origin, context) };
		const status = context.daemonStatus;
		// Select the owner once. A stopped daemon may use the independent helper,
		// but a normal session's uncertain launch must never start another agent.
		if (status.state === "ready") return this.startSession(start, status);
		if (start.preparationId) {
			const prepared = await this.get(start.preparationId);
			if (prepared.sessionId) return prepared;
			if (prepared.execution === "session") {
				if (prepared.sessionLaunchStarted) throw new Error("The standalone investigation may already exist. Reconnect to the daemon and press Start investigation again to reopen the same session.");
				return (await this.request({ type: "start", start: { ...prepared.request, preparationId: prepared.id } }, "run")).run!;
			}
		}
		return (await this.request({ type: "start", start }, "run")).run!;
	}

	private async startSession(start: TriageStartRequest, status: DaemonStatus): Promise<TriageRun> {
		if (!Number.isInteger(status.port) || !status.port || status.port < 1 || status.port > 65535 || !Number.isInteger(status.pid) || !status.pid || status.pid < 1) throw new Error("The daemon identity is not ready. Try again when AO reconnects.");
		if (!start.preparationId) start.preparationId = (await this.prepare(start.origin ?? {})).id;
		const handoff = await this.request({ type: "handoff", start }, "session-launch");
		if (handoff.run!.sessionId) return handoff.run!;
		const launch = handoff.launch!;
		const request = this.options.requestDaemon ?? requestDaemon;
		let postIntentAttempted = false;
		let createdSessionId: string | undefined;
		let intentRun = handoff.run!;
		const sameDaemon = () => {
			const current = this.options.context().daemonStatus;
			return current.state === "ready" && current.pid === status.pid && current.port === status.port && current.executablePath === status.executablePath;
		};
		try {
			const health = await request(status.port, "GET", "/healthz");
			if (health.status !== 200 || health.body.status !== "ok" || health.body.service !== "agent-orchestrator-daemon" || health.body.pid !== status.pid ||
				(status.executablePath && health.body.executablePath !== status.executablePath) || !sameDaemon()) throw new Error("The daemon identity changed before the investigation started.");
			postIntentAttempted = true;
			intentRun = (await this.request({ type: "session-create-intent", runId: handoff.run!.id }, "run")).run!;
			if (!sameDaemon()) throw new Error("The daemon changed while the investigation was starting.");
			const selected = handoff.run!.request;
			const response = await request(status.port, "POST", "/api/v1/sessions", {
				clientRequestId: launch.clientRequestId, kind: "worker", harness: selected.harness,
				displayName: "Report a Problem", mode: selected.sessionMode ?? "chat", prompt: launch.prompt, attachments: launch.attachments,
				...(selected.model ? { model: selected.model } : {}), ...(selected.effort ? { effort: selected.effort } : {}),
				...(selected.permissions ? { approvalMode: selected.permissions } : {}),
			});
			if (response.status < 200 || response.status >= 300) {
				const error = response.body.error && typeof response.body.error === "object" ? record(response.body.error) : {};
				throw new Error(typeof error.message === "string" ? scrubTriageDiagnostics(error.message) : `The daemon returned HTTP ${response.status}.`);
			}
			const session = record(response.body.session);
			const sessionId = text(session.id, 255, true);
			createdSessionId = sessionId;
			const linked = (await this.request({ type: "session-linked", runId: handoff.run!.id, sessionId }, "run")).run!;
			if (linked.id !== handoff.run!.id || linked.sessionId !== sessionId) throw new Error("The helper could not confirm the standalone session link.");
			return linked;
		} catch (error) {
			if (createdSessionId) {
				// A failed manifest link cannot turn a successful ordinary create
				// into an uncertain launch. The persisted create key remains safe
				// to replay if this app exits before linkage can be saved.
				return { ...intentRun, sessionId: createdSessionId };
			}
			if (!postIntentAttempted && !handoff.run!.sessionLaunchStarted) {
				// No create request could have been sent. The helper checks the
				// durable intent again before adopting this same run offline.
				return (await this.request({ type: "start", start: { ...handoff.run!.request, preparationId: handoff.run!.id } }, "run")).run!;
			}
			const reason = error instanceof Error ? scrubTriageDiagnostics(error.message) : "The session request failed.";
			throw new Error(`${reason} Press Start investigation again to check or retry the same standalone session.`);
		}
	}
	async reply(runId: string, value: string, input?: TriageReplyInput): Promise<TriageRun> {
		const id = text(runId, 128, true);
		const response = await this.request({ type: "reply", runId: id, text: text(value, MAX_TRIAGE_COMMAND_BYTES),
			...(input ? { ...input, requestId: text(input.requestId, 128, true) } : {}) }, ["run", "ack"]);
		return response.run ?? this.get(id);
	}
	async cancel(runId: string): Promise<TriageRun> { return (await this.request({ type: "cancel", runId: text(runId, 128, true) }, "run")).run!; }
	async terminalInput(runId: string, value: string): Promise<void> {
		if (typeof value !== "string" || Buffer.byteLength(value) > MAX_TRIAGE_COMMAND_BYTES) throw new Error("Invalid terminal input.");
		await this.request({ type: "terminal-input", runId: text(runId, 128, true), text: value }, "ack");
	}
	async terminalResize(runId: string, rows: number, cols: number): Promise<void> {
		if (!Number.isInteger(rows) || rows < 1 || rows > 1000 || !Number.isInteger(cols) || cols < 1 || cols > 1000) throw new Error("Invalid terminal size.");
		await this.request({ type: "terminal-resize", runId: text(runId, 128, true), rows, cols }, "ack");
	}
	subscribe(listener: (run: TriageRun) => void): () => void {
		this.listeners.add(listener);
		return () => { this.listeners.delete(listener); };
	}
	subscribeTerminal(listener: (output: TriageTerminalOutput) => void): () => void {
		this.terminalListeners.add(listener);
		return () => { this.terminalListeners.delete(listener); };
	}

	shutdown(): Promise<void> {
		if (this.shutdownPromise) return this.shutdownPromise;
		this.closing = true;
		const child = this.child;
		if (!child) return Promise.resolve();
		this.shutdownPromise = (async () => {
			const timeout = this.options.shutdownTimeoutMs ?? 1500;
			try { await this.request({ type: "quit" }, "quit", timeout, true); } catch { /* EOF also persists an interrupted run. */ }
			child.stdin.end();
			let deadline: ReturnType<typeof setTimeout> | undefined;
			await Promise.race([this.exited, new Promise<void>((resolve) => {
				deadline = setTimeout(() => { (this.options.terminateProcess ?? terminateHelper)(child); resolve(); }, timeout);
			})]);
			if (deadline) clearTimeout(deadline);
			if (this.terminationTimer) clearTimeout(this.terminationTimer);
		})();
		return this.shutdownPromise;
	}

	closeOnExit(): void {
		if (this.child) { this.child.stdin.end(); (this.options.terminateProcess ?? terminateHelper)(this.child); }
	}
}

/** IPC is registered once and accepts only the app's trusted shell contents. */
export function registerTriageIpc(input: {
	ipcMain: Pick<IpcMain, "handle">;
	service: TriageService;
	isTrustedSender: (event: Parameters<Parameters<IpcMain["handle"]>[1]>[0]) => boolean;
}): void {
	const methods = ["catalog", "configure", "list", "get", "prepare", "start", "reply", "cancel", "terminalInput", "terminalResize"] as const;
	for (const name of methods) {
		input.ipcMain.handle(TRIAGE_CHANNELS[name], (event, ...args: unknown[]) => {
			if (!input.isTrustedSender(event)) throw new Error("Untrusted support request.");
			return Reflect.apply(input.service[name], input.service, args);
		});
	}
}
