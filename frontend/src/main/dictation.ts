import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { app, ipcMain, type WebContents } from "electron";
import type { DictationAuthorization, DictationEvent } from "../shared/dictation";

// One dictation at a time, run by the native helper and streamed to the shell
// renderer. Returns `stop`, which finishes a running dictation and keeps its text.
export function registerDictationIpc(helperPath: string, shellWebContents: () => WebContents | null): { stop(): void } {
	let helper: ChildProcess | null = null;
	const stop = () => {
		helper?.stdin?.write("stop\n");
	};
	app.on("will-quit", () => helper?.kill());

	// Asking macOS for access shows its system prompts, which take focus from the
	// app window, and losing focus stops dictation (see main.ts). So the asking is
	// done by a helper of its own that nothing else can stop, one process for both
	// prompts, before any dictation starts. Granted access is remembered for the
	// session; macOS itself never asks again once the user has answered.
	let authorized = false;
	let authorizing: Promise<DictationAuthorization> | null = null;
	const authorize = (): Promise<DictationAuthorization> =>
		authorized
			? Promise.resolve({ granted: true })
			: (authorizing ??= new Promise<DictationAuthorization>((resolve) => {
					const child = spawn(helperPath, ["--authorize"], { stdio: ["ignore", "pipe", "ignore"] });
					let answer: DictationAuthorization | undefined;
					createInterface({ input: child.stdout! }).on("line", (line) => {
						try {
							const message = JSON.parse(line) as { type?: string; granted?: boolean; message?: string };
							if (message.type === "authorization")
								answer = { granted: message.granted === true, message: message.message };
						} catch {
							// Not a protocol line.
						}
					});
					app.once("will-quit", () => child.kill());
					// Nobody is left to answer the prompts after this long.
					const timeout = setTimeout(() => child.kill(), 180_000);
					child.on("error", () => {
						clearTimeout(timeout);
						resolve({ granted: false, message: "Dictation is not available." });
					});
					child.on("close", () => {
						clearTimeout(timeout);
						const result = answer ?? {
							granted: false,
							message: "Could not check microphone and speech recognition access.",
						};
						if (result.granted) authorized = true;
						resolve(result);
					});
				}).finally(() => {
					authorizing = null;
				}));

	ipcMain.handle("dictation:authorize", (event): Promise<DictationAuthorization> | DictationAuthorization => {
		if (process.platform !== "darwin" || event.sender !== shellWebContents()) return { granted: true };
		return authorize();
	});

	// A newer start or a cancel while permissions are still being asked for abandons the older start.
	let startSerial = 0;
	ipcMain.handle("dictation:start", async (event) => {
		if (process.platform !== "darwin" || event.sender !== shellWebContents()) return;
		const sender = event.sender;
		const serial = ++startSerial;
		if (!authorized) {
			const access = await authorize();
			if (serial !== startSerial || sender.isDestroyed()) return;
			if (!access.granted) {
				sender.send("dictation:event", {
					type: "error",
					message: access.message ?? "Dictation is not available.",
				} satisfies DictationEvent);
				return;
			}
		}
		const previous = helper;
		const child = spawn(helperPath, [], { stdio: ["pipe", "pipe", "ignore"] });
		helper = child;
		// A helper that was replaced or cancelled is still shutting down; its
		// events, exit included, belong to a dictation that already ended.
		const send = (message: DictationEvent) => {
			if (helper === child && !sender.isDestroyed()) sender.send("dictation:event", message);
		};
		previous?.kill();
		// A stop written while the helper is exiting would otherwise throw EPIPE in main.
		child.stdin?.on("error", () => undefined);
		createInterface({ input: child.stdout! }).on("line", (line) => {
			try {
				const event = JSON.parse(line) as DictationEvent;
				// An error may mean macOS access was taken away since it was granted: check again next time.
				if (event.type === "error") authorized = false;
				send(event);
			} catch {
				// Not a protocol line.
			}
		});
		child.on("error", () => send({ type: "error", message: "Dictation is not available." }));
		// "close", not "exit": exit can arrive before the last stdout lines, so the
		// renderer would hear "exit" ahead of the helper's final "end".
		child.on("close", () => {
			send({ type: "exit" });
			if (helper === child) helper = null;
		});
	});

	ipcMain.handle("dictation:stop", (event) => {
		if (event.sender === shellWebContents()) stop();
	});

	ipcMain.handle("dictation:cancel", (event) => {
		if (event.sender !== shellWebContents()) return;
		startSerial += 1;
		helper?.stdin?.write("cancel\n");
		helper = null;
	});

	return { stop };
}
