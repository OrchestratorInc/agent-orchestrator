import type { MutableRefObject, ReactNode } from "react";

export type ArtifactMenuAction = {
	id: string;
	label: string;
	icon: ReactNode;
	onSelect: () => void;
	disabled?: boolean;
	mode?: { checked: boolean };
};

export type ArtifactMenuActions = MutableRefObject<ArtifactMenuAction[]>;
