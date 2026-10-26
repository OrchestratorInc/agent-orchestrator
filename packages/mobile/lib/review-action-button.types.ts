import type { ReviewDrawableName } from "./review-drawables";
import type { IOSSymbol } from "./symbols";

/**
 * `filled` for the row's live action, `outlined` for the others, and
 * `destructive` for one that stops something already running.
 */
export type ReviewActionButtonTone = "filled" | "outlined" | "destructive";

export type ReviewActionButtonProps = {
	label: string;
	/** The SF Symbol iOS draws and the bundled Material Symbol Android draws. */
	icon: { ios: IOSSymbol; android: ReviewDrawableName };
	tone: ReviewActionButtonTone;
	/** The request is in flight: a spinner in place of the glyph, and no second press. */
	busy?: boolean;
	disabled?: boolean;
	onPress: () => void;
};
