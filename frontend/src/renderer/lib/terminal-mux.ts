type ServerFrame = {
	ch: string;
	id?: string;
	type: string;
	data?: string;
	error?: string;
};

export function bytesToBase64(bytes: Uint8Array): string {
	let binary = "";
	const chunk = 0x8000;
	for (let i = 0; i < bytes.length; i += chunk) {
		binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
	}
	return btoa(binary);
}

export function base64ToBytes(b64: string): Uint8Array {
	const binary = atob(b64);
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i += 1) {
		bytes[i] = binary.charCodeAt(i);
	}
	return bytes;
}

export function openFrame(id: string, cols: number, rows: number): string {
	return JSON.stringify({ ch: "terminal", type: "open", id, cols, rows });
}

export function dataFrame(id: string, bytes: Uint8Array): string {
	return JSON.stringify({ ch: "terminal", type: "data", id, data: bytesToBase64(bytes) });
}

export function resizeFrame(id: string, cols: number, rows: number, force = false): string {
	return JSON.stringify({
		ch: "terminal",
		type: "resize",
		id,
		cols,
		rows,
		...(force ? { force: true } : {}),
	});
}

export function closeFrame(id: string): string {
	return JSON.stringify({ ch: "terminal", type: "close", id });
}

function pingFrame(): string {
	return JSON.stringify({ ch: "system", type: "ping" });
}

export function muxUrlFromApiBase(apiBaseUrl: string): string {
	if (apiBaseUrl === "" && typeof window !== "undefined") {
		const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
		return `${protocol}//${window.location.host}/mux`;
	}

	const ws = apiBaseUrl.replace(/^http/i, "ws");
	return `${ws.replace(/\/+$/, "")}/mux`;
}

type DataListener = (bytes: Uint8Array) => void;
type ExitListener = () => void;
type OpenedListener = () => void;
type ErrorListener = (message: string) => void;

export type MuxConnectionState = "open" | "closed" | "waiting";
type ConnectionListener = (state: MuxConnectionState) => void;

export type TerminalMux = {
	open: (id: string, cols: number, rows: number) => void;
	sendInput: (id: string, input: string) => void;
	resize: (id: string, cols: number, rows: number, force?: boolean) => void;
	close: (id: string) => void;
	onData: (id: string, listener: DataListener) => () => void;
	onExit: (id: string, listener: ExitListener) => () => void;
	onOpened: (id: string, listener: OpenedListener) => () => void;
	onError: (id: string, listener: ErrorListener) => () => void;
	onConnectionChange: (listener: ConnectionListener) => () => void;
	dispose: () => void;
};

export type TerminalMuxPool = {
	acquire: () => TerminalMux;
	dispose: () => void;
};

const PING_INTERVAL_MS = 20_000;
const MAX_INPUT_CHUNK_BYTES = 32 * 1024;

function subscribeById<T>(map: Map<string, Set<T>>, id: string, listener: T): () => void {
	const set = map.get(id) ?? new Set<T>();
	set.add(listener);
	map.set(id, set);
	return () => set.delete(listener);
}

export function createTerminalMux(url: string, WebSocketImpl: typeof WebSocket = WebSocket): TerminalMux {
	const socket = new WebSocketImpl(url);
	const encoder = new TextEncoder();
	const queue: string[] = [];
	const dataListeners = new Map<string, Set<DataListener>>();
	const exitListeners = new Map<string, Set<ExitListener>>();
	const openedListeners = new Map<string, Set<OpenedListener>>();
	const errorListeners = new Map<string, Set<ErrorListener>>();
	const connectionListeners = new Set<ConnectionListener>();
	let connectionState: MuxConnectionState | undefined;
	let pingTimer: ReturnType<typeof setInterval> | undefined;
	let disposed = false;

	const setConnectionState = (next: MuxConnectionState) => {
		if (disposed || connectionState === next) return;
		connectionState = next;
		connectionListeners.forEach((listener) => listener(next));
	};

	const flush = () => {
		while (queue.length > 0) {
			const frame = queue.shift();
			if (frame !== undefined) socket.send(frame);
		}
	};

	const send = (frame: string) => {
		if (disposed) return;
		if (socket.readyState === WebSocketImpl.OPEN) {
			socket.send(frame);
		} else {
			queue.push(frame);
		}
	};

	socket.addEventListener("open", () => {
		if (disposed) return;
		flush();
		pingTimer = setInterval(() => send(pingFrame()), PING_INTERVAL_MS);
		setConnectionState("open");
	});

	socket.addEventListener("close", () => {
		setConnectionState("closed");
	});
	socket.addEventListener("error", () => {
		setConnectionState("closed");
	});

	socket.addEventListener("message", (event: MessageEvent) => {
		if (typeof event.data !== "string") return;
		let frame: ServerFrame;
		try {
			frame = JSON.parse(event.data) as ServerFrame;
		} catch {
			return;
		}
		if (frame.ch !== "terminal") return;
		if (frame.type === "error") {
			const message = frame.error ?? "unknown terminal error";
			if (frame.id !== undefined) {
				errorListeners.get(frame.id)?.forEach((listener) => listener(message));
			} else {
				errorListeners.forEach((set) => set.forEach((listener) => listener(message)));
			}
			return;
		}
		if (frame.id === undefined) return;
		if (frame.type === "data" && frame.data) {
			dataListeners.get(frame.id)?.forEach((listener) => listener(base64ToBytes(frame.data as string)));
		} else if (frame.type === "exited") {
			exitListeners.get(frame.id)?.forEach((listener) => listener());
		} else if (frame.type === "opened") {
			openedListeners.get(frame.id)?.forEach((listener) => listener());
		}
	});

	const dispose = () => {
		if (disposed) return;
		disposed = true;
		if (pingTimer) clearInterval(pingTimer);
		dataListeners.clear();
		exitListeners.clear();
		openedListeners.clear();
		errorListeners.clear();
		connectionListeners.clear();
		try {
			socket.close();
		} catch {
		}
	};

	return {
		open: (id, cols, rows) => {
			send(openFrame(id, cols, rows));
		},
		sendInput: (id, input) => {
			if (disposed) return;
			const bytes = encoder.encode(input);
			let offset = 0;
			do {
				send(dataFrame(id, bytes.subarray(offset, offset + MAX_INPUT_CHUNK_BYTES)));
				offset += MAX_INPUT_CHUNK_BYTES;
			} while (offset < bytes.length);
		},
		resize: (id, cols, rows, force) => {
			send(resizeFrame(id, cols, rows, force));
		},
		close: (id) => {
			send(closeFrame(id));
		},
		onData: (id, listener) => subscribeById(dataListeners, id, listener),
		onExit: (id, listener) => subscribeById(exitListeners, id, listener),
		onOpened: (id, listener) => subscribeById(openedListeners, id, listener),
		onError: (id, listener) => subscribeById(errorListeners, id, listener),
		onConnectionChange: (listener) => {
			connectionListeners.add(listener);
			return () => connectionListeners.delete(listener);
		},
		dispose,
	};
}

export function createTerminalMuxPool(createMux: () => TerminalMux): TerminalMuxPool {
	type Connection = {
		closed: boolean;
		disposed: boolean;
		mux: TerminalMux;
		refs: number;
		unsubscribeState: () => void;
	};

	const connections = new Set<Connection>();
	let current: Connection | null = null;

	const disposeConnection = (connection: Connection) => {
		if (connection.disposed) return;
		connection.disposed = true;
		connection.closed = true;
		if (current === connection) current = null;
		connections.delete(connection);
		connection.unsubscribeState();
		connection.mux.dispose();
	};

	const newConnection = (): Connection => {
		const mux = createMux();
		const connection: Connection = {
			closed: false,
			disposed: false,
			mux,
			refs: 0,
			unsubscribeState: () => undefined,
		};
		connection.unsubscribeState = mux.onConnectionChange((state) => {
			if (state !== "closed") return;
			connection.closed = true;
			if (current === connection) current = null;
			if (connection.refs === 0) disposeConnection(connection);
		});
		connections.add(connection);
		current = connection;
		return connection;
	};

	const acquire = (): TerminalMux => {
		const connection = current && !current.closed && !current.disposed ? current : newConnection();
		connection.refs += 1;
		let released = false;
		const subscriptions = new Set<() => void>();

		const subscribe = (register: () => () => void): (() => void) => {
			if (released || connection.closed || connection.disposed) return () => undefined;
			const unsubscribe = register();
			let subscribed = true;
			const dispose = () => {
				if (!subscribed) return;
				subscribed = false;
				subscriptions.delete(dispose);
				unsubscribe();
			};
			subscriptions.add(dispose);
			return dispose;
		};

		const dispose = () => {
			if (released) return;
			released = true;
			for (const unsubscribe of [...subscriptions]) unsubscribe();
			connection.refs -= 1;
			if (connection.refs === 0) disposeConnection(connection);
		};

		return {
			open: (id, cols, rows) => {
				if (!released && !connection.closed && !connection.disposed) connection.mux.open(id, cols, rows);
			},
			sendInput: (id, input) => {
				if (!released && !connection.closed && !connection.disposed) connection.mux.sendInput(id, input);
			},
			resize: (id, cols, rows, force) => {
				if (!released && !connection.closed && !connection.disposed) {
					connection.mux.resize(id, cols, rows, force);
				}
			},
			close: (id) => {
				if (!released && !connection.closed && !connection.disposed) connection.mux.close(id);
			},
			onData: (id, listener) => subscribe(() => connection.mux.onData(id, listener)),
			onExit: (id, listener) => subscribe(() => connection.mux.onExit(id, listener)),
			onOpened: (id, listener) => subscribe(() => connection.mux.onOpened(id, listener)),
			onError: (id, listener) => subscribe(() => connection.mux.onError(id, listener)),
			onConnectionChange: (listener) => subscribe(() => connection.mux.onConnectionChange(listener)),
			dispose,
		};
	};

	return {
		acquire,
		dispose: () => {
			current = null;
			for (const connection of [...connections]) disposeConnection(connection);
		},
	};
}
