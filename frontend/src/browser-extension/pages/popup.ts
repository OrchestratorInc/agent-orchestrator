import { chromeApi } from "../chrome-api";

type Status = {
	state: "connecting" | "connected" | "disconnected";
	error?: { code: string; message: string };
	version: string;
	activeCommands: number;
	sessions: { sessionId: string; tabs: number; approved: boolean; trusted: boolean }[];
};

const chrome = chromeApi();
const send = (message: Record<string, unknown>) => chrome.runtime.sendMessage(message);

async function render(): Promise<void> {
	const status = (await send({ type: "ao-ui-status" })) as Status;
	const dot = document.getElementById("dot")!;
	dot.className = `dot ${status.state === "connected" ? "ok" : "bad"}`;
	document.getElementById("state")!.textContent =
		status.state === "connected" ? (status.activeCommands > 0 ? "Agent working" : "Connected to AO") : status.state === "connecting" ? "Connecting to AO…" : "Not connected to AO";
	document.getElementById("detail")!.textContent = status.error?.message ?? `Version ${status.version}`;
	const list = document.getElementById("sessions")!;
	list.replaceChildren();
	if (status.sessions.length === 0) {
		const empty = document.createElement("span");
		empty.className = "muted";
		empty.textContent = "None yet.";
		list.append(empty);
	}
	for (const session of status.sessions) {
		const row = document.createElement("div");
		row.className = "row";
		const label = document.createElement("span");
		label.className = "mono";
		label.textContent = session.sessionId;
		const meta = document.createElement("span");
		meta.className = "muted";
		meta.textContent = `${session.tabs} tab${session.tabs === 1 ? "" : "s"} · ${session.approved ? (session.trusted ? "real input" : "simulated input") : "not allowed"}`;
		row.append(label, meta);
		if (session.approved) {
			const revoke = document.createElement("button");
			revoke.type = "button";
			revoke.textContent = "Revoke";
			revoke.addEventListener("click", async () => {
				await send({ type: "ao-ui-revoke", sessionId: session.sessionId });
				await render();
			});
			row.append(revoke);
		}
		list.append(row);
	}
}

document.getElementById("stop")!.addEventListener("click", async () => {
	await send({ type: "ao-ui-stop-all" });
	await render();
});
document.getElementById("reconnect")!.addEventListener("click", async () => {
	await send({ type: "ao-ui-reconnect" });
	setTimeout(() => void render(), 500);
});
document.getElementById("options")!.addEventListener("click", () => window.open(chrome.runtime.getURL("options.html")));
void render();
setInterval(() => void render(), 2_000);
