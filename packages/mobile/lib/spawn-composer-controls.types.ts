import type { VoiceMode, VoiceState } from "./voice/types";
import type { SourceRef } from "./environment/scopedBoard";

export type SpawnComposerOption = {
	id: string;
	label: string;
};

export type SpawnComposerVoice = {
	state: VoiceState;
	mode: VoiceMode;
	onPressIn: () => void;
	onPressOut: () => void;
};

export type SpawnComposerControlsProps = {
	destinations: readonly { source: SourceRef; label: string; available: boolean; unavailableReason?: string | null }[];
	destination: SourceRef | null;
	onSelectDestination: (source: SourceRef) => void;
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
	onAttach: () => void;
	showAttachments?: boolean;
	showModels?: boolean;
	/** Dictation into the prompt: hold to talk, double-tap for hands-free. */
	voice: SpawnComposerVoice;
	onSpawn: () => void;
	busy: boolean;
	disabled: boolean;
};
