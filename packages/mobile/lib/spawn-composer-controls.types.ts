import type { ReactNode } from "react";
import type { VoiceMode, VoiceState } from "./voice/types";

export type SpawnComposerOption = {
	id: string;
	label: string;
	/** A project row's glyph: a folder, or the standalone agent's new-conversation mark. */
	icon?: "folder" | "message-square-plus";
	sectionBreakBefore?: boolean;
	/** The name where the whole label has no room, such as a control's face. */
	shortLabel?: string;
};

/**
 * One agent's models as its menu offers them: the first few (with the picked
 * one kept in), how many there are in all, and the catalog's own default.
 */
export type SpawnAgentModels = {
	models: readonly SpawnComposerOption[];
	total: number;
	/** The model the catalog marks as its default, when it names one. */
	defaultLabel?: string;
};

export type SpawnComposerVoice = {
	state: VoiceState;
	mode: VoiceMode;
	onPressIn: () => void;
	onPressOut: () => void;
	/** Starts or finishes hands-free dictation in one step, for screen readers. */
	onToggleLatched: () => void;
};

export type SpawnComposerControlsProps = {
	hosts: readonly SpawnComposerOption[];
	hostId: string | null;
	onSelectHost: (hostId: string) => void;
	projects: readonly SpawnComposerOption[];
	projectId: string | null;
	onSelectProject: (projectId: string) => void;
	agents: readonly SpawnComposerOption[];
	harness: string;
	onSelectHarness: (harness: string) => void;
	/** The picked agent's models. */
	models: readonly SpawnComposerOption[];
	/** Every agent's models, so a menu can offer an agent and its model in one step. */
	agentModels: Readonly<Record<string, SpawnAgentModels>>;
	/** "__auto__" for the default model, or the picked model's id. */
	modelSelection: string;
	modelLabel: string;
	/** Whether the task runs the agent's default model: nothing picked, and the project pins none. */
	usesDefaultModel: boolean;
	/** Opens an agent's whole catalog, for one too long to list in a menu. */
	onBrowseModels: (harness: string) => void;
	onSelectModel: (model: string) => void;
	/** Picks an agent and one of its models together. */
	onSelectAgentModel: (harness: string, model: string) => void;
	/** The approval modes the picked agent can start in, and the one it will. */
	approvalModes: readonly SpawnComposerOption[];
	approvalMode: string;
	onSelectApprovalMode: (mode: string) => void;
	/** The attach button offers both the photo library and the file picker. */
	onChoosePhoto: () => void;
	onChooseFile: () => void;
	/** Dictation into the prompt: hold to talk, double-tap for hands-free. */
	voice: SpawnComposerVoice;
	/** The task's prompt, written in the composer's field. */
	prompt: string;
	onChangePrompt: (value: string) => void;
	/** Names who will answer, as a conversation's field does: "Ask Codex". */
	placeholder: string;
	/** The picked files, shown above the composer as the chat shows its own. */
	attachments: ReactNode;
	hasAttachments: boolean;
	onSpawn: () => void;
	busy: boolean;
	disabled: boolean;
};
