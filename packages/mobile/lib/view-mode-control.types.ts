export type ViewMode = "rendered" | "source";

export type ViewModeControlProps = {
	mode: ViewMode;
	/** What the rendered view is called: "Table", "Diagram". */
	renderedLabel: string;
	onChange: (mode: ViewMode) => void;
};

export const viewModes: readonly ViewMode[] = ["rendered", "source"];

export function viewModeLabel(mode: ViewMode, renderedLabel: string): string {
	return mode === "rendered" ? renderedLabel : "Source";
}
