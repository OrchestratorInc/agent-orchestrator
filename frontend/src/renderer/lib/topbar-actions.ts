/** Every control in a session's top bar, so usage can decide what stays there. */
export const TOPBAR_ACTIONS = [
	"new_task",
	"open_board",
	"open_orchestrator",
	"open_editor",
	"editor_menu",
	"run_cue",
	"inspector",
	"notifications",
] as const;
export type TopbarAction = (typeof TOPBAR_ACTIONS)[number];

export const TOPBAR_VIEWS = ["worker", "orchestrator"] as const;
export type TopbarView = (typeof TOPBAR_VIEWS)[number];
