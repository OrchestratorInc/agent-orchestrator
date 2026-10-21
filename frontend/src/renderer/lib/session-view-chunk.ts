// SessionView is lazy so it stays off the startup path. The shell renders it
// (one instance for project and scratchpad sessions) and preloads it when idle;
// the session routes await it so a first open never commits a blank pane.
export const loadSessionView = () => import("../components/SessionView");
