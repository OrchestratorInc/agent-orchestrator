type SessionViewModule = typeof import("../components/SessionView");
let loaded: SessionViewModule | undefined;
export const loadSessionView = (): Promise<SessionViewModule> =>
	import("../components/SessionView").then((module) => (loaded = module));
export const loadedSessionView = (): SessionViewModule | undefined => loaded;
