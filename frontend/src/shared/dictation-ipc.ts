import type { IpcRenderer, IpcRendererEvent } from "electron";
import type { DictationMessage } from "./dictation";

/** A surface's line to main's dictation helper (see main/dictation). */
export type DictationTransport = {
	/** Starts a run; main tags its events with `run`. */
	start(run: number): Promise<void>;
	/** Ends the audio; the final wording follows as "end". */
	stop(): Promise<void>;
	/** Ends the run at once. */
	cancel(): Promise<void>;
	onEvent(listener: (event: DictationMessage) => void): () => void;
};

/** The transport over a preload's ipcRenderer: the shell's bridge and the annotation overlay share it. */
export function dictationIpc(ipc: Pick<IpcRenderer, "invoke" | "on" | "removeListener">): DictationTransport {
	return {
		start: (run) => ipc.invoke("dictation:start", run) as Promise<void>,
		stop: () => ipc.invoke("dictation:stop") as Promise<void>,
		cancel: () => ipc.invoke("dictation:cancel") as Promise<void>,
		onEvent: (listener) => {
			const handler = (_event: IpcRendererEvent, message: DictationMessage) => listener(message);
			ipc.on("dictation:event", handler);
			return () => {
				ipc.removeListener("dictation:event", handler);
			};
		},
	};
}
