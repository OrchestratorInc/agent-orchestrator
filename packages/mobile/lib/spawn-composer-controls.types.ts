import type { VoiceMode, VoiceState } from "./voice/types";

export type SpawnComposerOption = {
	id: string;
	label: string;
	icon?: "folder" | "message-square-plus" | "image" | "file-text";
	sectionBreakBefore?: boolean;
};

export type SpawnComposerVoice = {
	state: VoiceState;
	mode: VoiceMode;
	onPressIn: () => void;
	onPressOut: () => void;
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
	models: readonly SpawnComposerOption[];
	modelSelection: string;
	modelLabel: string;
	onSelectModel: (model: string) => void;
	/** The attach button offers both the photo library and the file picker. */
	onChoosePhoto: () => void;
	onChooseFile: () => void;
	/** Dictation into the prompt: hold to talk, double-tap for hands-free. */
	voice: SpawnComposerVoice;
	onSpawn: () => void;
	busy: boolean;
	disabled: boolean;
};
