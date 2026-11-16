// SessionView is lazy so it stays off the startup path. The shell renders it
// (one instance for project and scratchpad sessions) and preloads it when idle;
// the session routes await it so a first open never commits a blank pane.
type SessionViewModule = typeof import("../components/SessionView");
let loaded: SessionViewModule | undefined;
export const loadSessionView = (): Promise<SessionViewModule> =>
	import("../components/SessionView").then((module) => (loaded = module));
/** The module once it has loaded, so a first render can use it without suspending. */
export const loadedSessionView = (): SessionViewModule | undefined => loaded;
