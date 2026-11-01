import type { IOSSymbol } from "./symbols";
import type { MaterialDrawableName } from "./toolbarIcon.types";

/**
 * `filled` for the row's live action, `outlined` for the others, and
 * `destructive` for one that stops something already running.
 */
export type ReviewActionButtonTone = "filled" | "outlined" | "destructive";

export type ReviewActionButtonProps = {
	label: string;
	/** What a screen reader says when several buttons read the same, such as two Opens. */
	accessibilityLabel?: string;
	/** The SF Symbol iOS draws and the bundled Material Symbol Android draws. */
	icon: { ios: IOSSymbol; android: MaterialDrawableName };
	tone: ReviewActionButtonTone;
	/** The request is in flight: a spinner in place of the glyph, and no second press. */
	busy?: boolean;
	disabled?: boolean;
	onPress: () => void;
};
