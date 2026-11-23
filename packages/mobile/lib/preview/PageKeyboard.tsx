import { useEffect, useRef, useState } from "react";
import { StyleSheet, Text, TextInput, View } from "react-native";
import { KeyboardStickyView } from "react-native-keyboard-controller";
import type { BrowserControl, FocusedEditable } from "../browserTransport";
import { textSelectionColors } from "../textSelection";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { space, type } from "../tokens";
import { Button } from "../ui";
import { canType, keyboardTraits, type PageTyping } from "./pageTyping";

export type PageKeyboardProps = {
	typing: PageTyping;
	focused: FocusedEditable;
	control: BrowserControl;
	/** Changes with each new stream, which starts the field again from the page. */
	connection: number;
	/** Changes with each tap on the page, which opens a dismissed keyboard again. */
	openRequest: number;
};

const FIELD_NAMES: Record<NonNullable<FocusedEditable>["kind"], string> = {
	text: "text field",
	password: "password field",
	email: "email field",
	number: "number field",
	url: "address field",
	search: "search field",
	tel: "phone number field",
	textarea: "text area",
	contenteditable: "text area",
};

/**
 * The phone's keyboard for the desktop page's focused field: a hidden field
 * whose changes are typed into the page, with a bar above the keyboard to put
 * it away. What is typed is never logged or shown, a password least of all.
 */
export function PageKeyboard({ typing, focused, control, connection, openRequest }: PageKeyboardProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const input = useRef<TextInput>(null);
	const [text, setText] = useState("");
	const [dismissed, setDismissed] = useState(false);

	useEffect(() => {
		const reset = typing.observe(focused, connection, control);
		if (reset !== null) setText(reset);
	}, [connection, control, focused, typing]);
	useEffect(() => {
		setDismissed(false);
		input.current?.focus();
	}, [openRequest]);

	if (!focused || !canType(focused, control) || dismissed) return null;
	const traits = keyboardTraits(focused.kind);
	const field = FIELD_NAMES[focused.kind];
	return <>
		<TextInput
			ref={input}
			autoFocus
			value={text}
			onChangeText={(next) => setText(typing.change(next))}
			// An empty field reports no change for Backspace, but the page's may hold text.
			onKeyPress={({ nativeEvent }) => { if (nativeEvent.key === "Backspace" && text === "") typing.key("Backspace"); }}
			onSubmitEditing={() => typing.key("Enter")}
			submitBehavior={traits.multiline ? "newline" : "submit"}
			{...traits}
			{...textSelectionColors(t)}
			accessibilityLabel={`Desktop page ${field}`}
			style={styles.hidden}
		/>
		<KeyboardStickyView style={styles.sticky}>
			<View style={styles.bar}>
				<Text style={styles.label} numberOfLines={1}>Typing into the page's {field}</Text>
				<Button title="Done" variant="ghost" onPress={() => { input.current?.blur(); setDismissed(true); }} />
			</View>
		</KeyboardStickyView>
	</>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	// Off screen in all but name: the frame shows what is typed, as the page draws it.
	hidden: { position: "absolute", width: 1, height: 1, opacity: 0, left: -1, top: -1 },
	sticky: { position: "absolute", left: 0, right: 0, bottom: 0 },
	bar: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		paddingHorizontal: space.md,
		paddingVertical: space.sm,
		backgroundColor: t.bgElevated,
		borderTopWidth: StyleSheet.hairlineWidth,
		borderTopColor: t.borderSubtle,
	},
	label: { ...type.footnote, flex: 1, color: t.textSecondary },
});
