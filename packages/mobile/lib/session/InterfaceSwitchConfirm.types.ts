/** How a busy session gets to Chat: after its turn, or by stopping it. */
export type InterfaceSwitchPolicy = "drain" | "interrupt";

export type InterfaceSwitchConfirmProps = {
	/** What the question says while it is being asked; null when nothing is asked. */
	message: string | null;
	onChoose: (policy: InterfaceSwitchPolicy) => void;
	/** The user kept the terminal. Also called once an answer has closed the question. */
	onCancel: () => void;
};

export const INTERFACE_SWITCH_CONFIRM = {
	title: "Switch to Chat?",
	drain: "Finish, then switch",
	interrupt: "Stop and switch",
	cancel: "Keep Terminal UI",
} as const;
