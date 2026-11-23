import type { KeyboardTypeOptions, ReturnKeyTypeOptions, TextInputProps } from "react-native";
import type { BrowserCommand, BrowserControl, FocusedEditable } from "../browserTransport";
import { textEdits } from "./textDiff";

type EditableKind = NonNullable<FocusedEditable>["kind"];
type PageKey = "Enter" | "Tab" | "Backspace" | "ArrowLeft" | "ArrowRight";

/** The desktop refuses text over 16 KiB, so a paste goes in pieces under it. */
export const TEXT_CHUNK = 4000;

export type KeyboardTraits = Pick<
	TextInputProps,
	"autoCapitalize" | "autoComplete" | "autoCorrect" | "importantForAutofill" | "spellCheck" | "textContentType"
> & { keyboardType: KeyboardTypeOptions; returnKeyType: ReturnKeyTypeOptions; secureTextEntry: boolean; multiline: boolean };

const KEYBOARDS: Partial<Record<EditableKind, KeyboardTypeOptions>> = {
	email: "email-address",
	number: "decimal-pad",
	tel: "phone-pad",
	url: "url",
	search: "web-search",
};

/** The phone's keyboard for the page's field. */
export function keyboardTraits(kind: EditableKind): KeyboardTraits {
	if (kind === "password") {
		// The phone keeps nothing: no suggestions, no learning, no autofill.
		return {
			keyboardType: "default",
			returnKeyType: "go",
			secureTextEntry: true,
			multiline: false,
			autoCapitalize: "none",
			autoComplete: "off",
			autoCorrect: false,
			spellCheck: false,
			textContentType: "none",
			importantForAutofill: "no",
		};
	}
	const plain = kind === "email" || kind === "url" || kind === "number" || kind === "tel";
	return {
		keyboardType: KEYBOARDS[kind] ?? "default",
		returnKeyType: kind === "search" ? "search" : kind === "url" ? "go" : "default",
		secureTextEntry: false,
		multiline: kind === "textarea" || kind === "contenteditable",
		autoCapitalize: plain ? "none" : "sentences",
		autoComplete: "off",
		autoCorrect: !plain,
		spellCheck: !plain,
		textContentType: "none",
		importantForAutofill: "no",
	};
}

/** The phone types only into a focused field, and only while the person holds control. */
export function canType(focused: FocusedEditable, control: BrowserControl): boolean {
	return focused !== null && control === "human";
}

/**
 * Turns what the person types into page input. Edits are relative to what the
 * phone already typed, with the page's caret at their end, so an echo of the
 * page's value never duplicates or drops a character. The field starts again
 * from the page's value whenever its field may have changed: a new stream, a
 * field of another kind, a tap that moves focus, or control coming back.
 */
export class PageTyping {
	private baseline = "";
	private connection: unknown = undefined;
	private connected = false;
	private control: BrowserControl = "none";
	private focused: FocusedEditable = null;
	private reported = "null";
	private tapPending = false;

	constructor(private readonly send: (command: BrowserCommand) => void) {}

	/** Takes the latest page state. Returns the field's new text when it starts again, else null. */
	observe(focused: FocusedEditable, connection: unknown, control: BrowserControl): string | null {
		const reported = JSON.stringify(focused);
		const reset = !this.connected ||
			connection !== this.connection ||
			(focused?.kind ?? null) !== (this.focused?.kind ?? null) ||
			(this.tapPending && reported !== this.reported) ||
			(control === "human" && this.control !== "human");
		if (reset && reported !== this.reported) this.tapPending = false;
		this.connection = connection;
		this.connected = true;
		this.control = control;
		this.focused = focused;
		this.reported = reported;
		if (!reset) return null;
		this.baseline = focused?.value ?? "";
		return this.baseline;
	}

	/** The stream dropped: nothing typed now can reach the page. */
	disconnect(): void {
		this.connected = false;
	}

	/** The person tapped the page, which may move its focus. */
	tapped(): void {
		this.tapPending = true;
	}

	/**
	 * Takes the field's new text. Returns the text it should show. Control is
	 * the latest the page reported, never a keystroke handler's older copy.
	 */
	change(next: string): string {
		if (!this.writable()) return this.baseline;
		for (const edit of textEdits(this.baseline, next)) {
			if (edit.kind === "key") this.press(edit.key);
			else this.type(edit.value);
		}
		this.baseline = next;
		return next;
	}

	key(key: PageKey): void {
		if (this.writable()) this.press(key);
	}

	private writable(): boolean {
		return this.connected && canType(this.focused, this.control);
	}

	private type(value: string): void {
		value.split("\n").forEach((line, index) => {
			if (index > 0) this.press("Enter");
			let start = 0;
			while (start < line.length) {
				let end = Math.min(line.length, start + TEXT_CHUNK);
				// Never split a surrogate pair across chunks.
				if (end < line.length && /[\uD800-\uDBFF]/.test(line[end - 1]!)) end--;
				this.send({ type: "input", payload: { kind: "text", value: line.slice(start, end) } });
				start = end;
			}
		});
	}

	private press(key: PageKey): void {
		this.send({ type: "input", payload: { kind: "key", phase: "down", key, code: key } });
		this.send({ type: "input", payload: { kind: "key", phase: "up", key, code: key } });
	}
}
