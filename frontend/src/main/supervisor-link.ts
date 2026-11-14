import net from "node:net";

const BACKOFF_INIT_MS = 200;
const BACKOFF_MAX_MS = 2_000;

export interface SupervisorLinkHandle {
	readonly connected: boolean;
	dispose(): void;
}

export function connectSupervisor(addr: string, opts?: { log?: (msg: string) => void }): SupervisorLinkHandle {
	const log = opts?.log ?? (() => undefined);

	let disposed = false;
	let connected = false;
	let socket: net.Socket | null = null;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let backoff = BACKOFF_INIT_MS;

	function clearRetry() {
		if (retryTimer !== null) {
			clearTimeout(retryTimer);
			retryTimer = null;
		}
	}

	function destroySocket() {
		if (socket !== null) {
			socket.removeAllListeners();
			socket.destroy();
			socket = null;
		}
	}

	function scheduleReconnect() {
		if (disposed) return;
		clearRetry();
		const delay = backoff;
		backoff = Math.min(backoff * 2, BACKOFF_MAX_MS);
		log(`supervisor-link: reconnecting in ${delay}ms`);
		retryTimer = setTimeout(() => {
			retryTimer = null;
			if (!disposed) connect();
		}, delay);
	}

	function connect() {
		if (disposed) return;

		destroySocket();

		const s = net.connect(addr);
		socket = s;

		s.on("connect", () => {
			if (disposed) {
				s.destroy();
				return;
			}
			connected = true;
			log("supervisor-link: connected");
			backoff = BACKOFF_INIT_MS;
		});

		s.on("data", () => undefined);

		s.on("error", (err) => {
			log(`supervisor-link: error: ${err.message}`);
		});

		s.on("close", () => {
			connected = false;
			if (disposed) return;
			log("supervisor-link: connection closed, will retry");
			scheduleReconnect();
		});
	}

	connect();

	return {
		get connected() {
			return connected;
		},
		dispose() {
			disposed = true;
			connected = false;
			clearRetry();
			destroySocket();
		},
	};
}
