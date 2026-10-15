/** React Native signals have abort events, but may lack throwIfAborted and reason. */
export function throwIfCloudRequestAborted(signal?: AbortSignal): void {
	if (signal?.aborted) {
		throw signal.reason !== undefined ? signal.reason : Object.assign(new Error("Cloud request cancelled."), { name: "AbortError" });
	}
}
