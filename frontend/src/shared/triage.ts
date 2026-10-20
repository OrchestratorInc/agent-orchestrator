// Private support-host protocol. Keep these fields aligned with
// backend/internal/triage/protocol.go; this is not a daemon API.
export const TRIAGE_CHANNELS = {
	catalog: "triage:catalog",
	configure: "triage:configure",
	list: "triage:list",
	get: "triage:get",
	prepare: "triage:prepare",
	start: "triage:start",
	reply: "triage:reply",
	cancel: "triage:cancel",
	terminalInput: "triage:terminal-input",
	terminalResize: "triage:terminal-resize",
	run: "triage:run",
	terminal: "triage:terminal",
} as const;

export type TriageChoice = {
	harness: string;
	label: string;
	models: Array<{ id: string; label: string; efforts?: string[]; default?: boolean }>;
	modelsMissing?: boolean;
	accounts?: Array<{ id: string; label: string; default?: boolean }>;
	mode?: string;
	permissions?: string;
	available: boolean;
	unavailableReason?: string;
	sessionMode?: "chat" | "tui";
};

export type TriageSelection = {
	harness: string;
	model?: string;
	effort?: string;
	accountId?: string;
	mode?: string;
	permissions?: string;
	sessionMode?: "chat" | "tui";
};

export type TriageOrigin = {
	appVersion?: string;
	buildMode?: string;
	hostId?: string;
	surface?: string;
	sessionId?: string;
	daemonState?: string;
	daemonCode?: string;
	daemonOutput?: string;
	machine?: Record<string, unknown>;
	session?: Record<string, unknown>;
};

export type TriageStartRequest = TriageSelection & {
	preparationId?: string;
	observation?: string;
	origin?: TriageOrigin;
	/** Lets the selected agent file through its normal GitHub tools. */
	publishAuthorized: boolean;
};

export type TriagePendingInput = {
	id: string;
	kind: "approval" | "user_input";
	summary: string;
	detail: Record<string, unknown>;
};

export type TriageRun = {
	id: string;
	version: number;
	createdAt: string;
	updatedAt: string;
	request: TriageStartRequest;
	state: string;
	activity: string;
	messages: Array<{ id: string; role: string; text: string; at: string }>;
	pendingInput?: TriagePendingInput;
	error?: string;
	preparation?: boolean;
	execution?: "session";
	sessionId?: string;
	sessionLaunchStarted?: boolean;
	transport?: "chat" | "terminal";
	/** Base64 of the last 64 KiB of terminal output. */
	terminalReplay?: string;
	/** Exclusive byte cursor for terminalReplay and terminal output events. */
	terminalSequence?: number;
};

export type TriageReplyInput = {
	requestId: string;
	decisionId?: string;
	action?: "accept" | "decline" | "cancel";
	content?: Record<string, unknown>;
};

export type TriageTerminalOutput = { runId: string; data: string; sequence: number };

export type TriageBridge = {
	catalog(): Promise<TriageChoice[]>;
	configure(choices: TriageChoice[]): Promise<void>;
	list(): Promise<TriageRun[]>;
	get(runId: string): Promise<TriageRun>;
	prepare(origin: TriageOrigin): Promise<TriageRun>;
	start(request: TriageStartRequest): Promise<TriageRun>;
	reply(runId: string, text: string, input?: TriageReplyInput): Promise<TriageRun>;
	cancel(runId: string): Promise<TriageRun>;
	terminalInput(runId: string, text: string): Promise<void>;
	terminalResize(runId: string, rows: number, cols: number): Promise<void>;
	subscribe(listener: (run: TriageRun) => void): () => void;
	subscribeTerminal(runId: string, listener: (data: string, sequence: number) => void): () => void;
};
