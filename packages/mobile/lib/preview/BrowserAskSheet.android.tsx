import { AlertDialog, Host, LazyColumn, ListItem, Text as NativeText, TextButton, TextField, useNativeState } from "@expo/ui/jetpack-compose";
import { alpha, clickable, fillMaxWidth, height } from "@expo/ui/jetpack-compose/modifiers";
import { useRef, useState, type ReactNode } from "react";
import { StyleSheet } from "react-native";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";
import { useTheme, useThemeState } from "../ThemeProvider";
import { font, stateOpacity } from "../tokens";
import { answerAuth, answerDialog, answerSelect, askTitle, authMessage } from "./browserAsk";
import { browserAskCopy as copy, type BrowserAskSheetProps } from "./BrowserAskSheet.types";
import { answerWithFiles, fileSources, filesAnswer } from "./browserAskAlerts";

export type { BrowserAskSheetProps } from "./BrowserAskSheet.types";

type Answer = (command: BrowserCommand | null) => void;

/**
 * What the page asks, on Android: the system's alert for an alert, a confirm,
 * leaving the page and where to pick files from; Material 3's dialog for a
 * prompt, a sign-in (its password field masked) and a select's options.
 */
export function BrowserAskSheet({ dialog, onAnswer, pickers }: BrowserAskSheetProps) {
	const latest = useRef({ onAnswer, pickers });
	latest.current = { onAnswer, pickers };
	// Answered, a dialog closes at once, with what was typed into it.
	const answered = useRef<BrowserDialog | null>(null);
	const [, closed] = useState(0);
	const answer = (asked: BrowserDialog): Answer => (command) => {
		if (answered.current === asked) return;
		answered.current = asked;
		closed((count) => count + 1);
		latest.current.onAnswer(asked, command);
	};

	if (!dialog || answered.current === dialog) return null;
	// Each is drawn only while it is the page's current ask, so one the host
	// closed or replaced goes away. Keyed by the dialog, so fields start fresh.
	if (dialog.kind === "alert" || dialog.kind === "confirm" || dialog.kind === "beforeunload") return <PageDialog key={keyOf(dialog)} dialog={dialog} answer={answer(dialog)} />;
	if (dialog.kind === "filechooser") return <FileSourceDialog key={keyOf(dialog)} dialog={dialog} answer={answer(dialog)} pickers={() => latest.current.pickers} />;
	if (dialog.kind === "prompt") return <PromptDialog key={keyOf(dialog)} dialog={dialog} answer={answer(dialog)} />;
	if (dialog.kind === "auth") return <AuthDialog key={keyOf(dialog)} dialog={dialog} answer={answer(dialog)} />;
	if (dialog.kind === "select") return <SelectDialog key={keyOf(dialog)} dialog={dialog} answer={answer(dialog)} />;
	return null;
}

let keys = new WeakMap<object, number>();
let nextKey = 0;
function keyOf(dialog: BrowserDialog): number {
	let key = keys.get(dialog);
	if (key === undefined) keys.set(dialog, (key = ++nextKey));
	return key;
}
/** Tests start each case from the same keys. */
export function resetBrowserAskKeys(): void {
	keys = new WeakMap();
	nextKey = 0;
}

function Frame({ title, onDismiss, confirm, dismiss, children }: { title: string; onDismiss(): void; confirm?: ReactNode; dismiss?: ReactNode; children: ReactNode }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host matchContents colorScheme={scheme} style={styles.host}>
			<AlertDialog onDismissRequest={onDismiss} colors={{ containerColor: t.bgElevated, titleContentColor: t.textPrimary, textContentColor: t.textSecondary }}>
				<AlertDialog.Title><NativeText style={{ typography: "headlineSmall", fontFamily: font.regular }}>{title}</NativeText></AlertDialog.Title>
				<AlertDialog.Text>{children}</AlertDialog.Text>
				{confirm ? <AlertDialog.ConfirmButton>{confirm}</AlertDialog.ConfirmButton> : null}
				{dismiss ? <AlertDialog.DismissButton>{dismiss}</AlertDialog.DismissButton> : null}
			</AlertDialog>
		</Host>
	);
}

function Button({ label, onClick, enabled = true }: { label: string; onClick(): void; enabled?: boolean }) {
	const t = useTheme();
	return (
		<TextButton enabled={enabled} onClick={onClick} colors={{ contentColor: t.accent, disabledContentColor: t.textFaint }}>
			<NativeText style={{ typography: "labelLarge", fontFamily: font.medium }}>{label}</NativeText>
		</TextButton>
	);
}

function useFieldColors() {
	const t = useTheme();
	return {
		textSelectionColors: { handleColor: t.highlight, backgroundColor: t.selection },
		colors: {
			focusedTextColor: t.textPrimary, unfocusedTextColor: t.textPrimary,
			focusedContainerColor: t.bgSubtle, unfocusedContainerColor: t.bgSubtle,
			cursorColor: t.highlight, focusedIndicatorColor: t.accent, unfocusedIndicatorColor: t.borderStrong,
			focusedLabelColor: t.accent, unfocusedLabelColor: t.textSecondary,
		},
	};
}

function PageDialog({ dialog, answer }: { dialog: Extract<BrowserDialog, { kind: "alert" | "confirm" | "beforeunload" }>; answer: Answer }) {
	const leave = dialog.kind === "beforeunload";
	const decline = () => answer(answerDialog(dialog.kind === "alert"));
	return (
		<Frame
			title={askTitle(dialog)}
			onDismiss={decline}
			confirm={<Button label={leave ? copy.leave : copy.ok} onClick={() => answer(answerDialog(true))} />}
			dismiss={dialog.kind === "alert" ? null : <Button label={leave ? copy.stay : copy.cancel} onClick={() => answer(answerDialog(false))} />}
		>
			<NativeText style={{ typography: "bodyMedium", fontFamily: font.regular }}>{leave ? copy.leaveMessage : dialog.message}</NativeText>
		</Frame>
	);
}

function FileSourceDialog({ dialog, answer, pickers }: { dialog: Extract<BrowserDialog, { kind: "filechooser" }>; answer: Answer; pickers: () => BrowserAskSheetProps["pickers"] }) {
	// Opening the system picker dismisses this dialog: once a source is
	// chosen, the dialog goes and only the picker answers.
	const chosen = useRef(false);
	const [picking, setPicking] = useState(false);
	const none = () => { if (!chosen.current) answer(filesAnswer(dialog, [])); };
	if (picking) return null;
	return (
		<Frame title={askTitle(dialog)} onDismiss={none} dismiss={<Button label={copy.cancel} onClick={none} />}>
			<LazyColumn modifiers={[fillMaxWidth()]}>
				{fileSources(dialog).map((source) => (
					<ListItem key={source} modifiers={[clickable(() => {
						chosen.current = true;
						setPicking(true);
						void answerWithFiles(source, dialog, answer, pickers());
					})]}>
						<ListItem.HeadlineContent>
							<NativeText style={{ typography: "bodyLarge", fontFamily: font.regular }}>{source === "photos" ? copy.photos : copy.files(dialog.multiple)}</NativeText>
						</ListItem.HeadlineContent>
					</ListItem>
				))}
			</LazyColumn>
		</Frame>
	);
}

function PromptDialog({ dialog, answer }: { dialog: Extract<BrowserDialog, { kind: "prompt" }>; answer: Answer }) {
	const text = useNativeState(dialog.defaultPrompt ?? "");
	const [value, setValue] = useState(dialog.defaultPrompt ?? "");
	const field = useFieldColors();
	return (
		<Frame
			title={askTitle(dialog)}
			onDismiss={() => answer(answerDialog(false))}
			confirm={<Button label={copy.ok} onClick={() => answer(answerDialog(true, value))} />}
			dismiss={<Button label={copy.cancel} onClick={() => answer(answerDialog(false))} />}
		>
			<TextField value={text} autoFocus singleLine onValueChange={setValue} textStyle={{ fontFamily: font.regular }} {...field}>
				{dialog.message ? <TextField.SupportingText><NativeText style={{ fontFamily: font.regular }}>{dialog.message}</NativeText></TextField.SupportingText> : null}
			</TextField>
		</Frame>
	);
}

function AuthDialog({ dialog, answer }: { dialog: Extract<BrowserDialog, { kind: "auth" }>; answer: Answer }) {
	const usernameText = useNativeState("");
	const passwordText = useNativeState("");
	// Held only until the dialog answers: never stored, logged or shown.
	const [username, setUsername] = useState("");
	const [password, setPassword] = useState("");
	const field = useFieldColors();
	const message = authMessage(dialog);
	return (
		<Frame
			title={askTitle(dialog)}
			onDismiss={() => answer(answerAuth(dialog, null))}
			confirm={<Button label={copy.signIn} onClick={() => answer(answerAuth(dialog, { username, password }))} />}
			dismiss={<Button label={copy.cancel} onClick={() => answer(answerAuth(dialog, null))} />}
		>
			<TextField
				value={usernameText}
				autoFocus
				singleLine
				onValueChange={setUsername}
				keyboardOptions={{ keyboardType: "text", autoCorrectEnabled: false, capitalization: "none", imeAction: "next" }}
				textStyle={{ fontFamily: font.regular }}
				{...field}
			>
				<TextField.Label><NativeText style={{ fontFamily: font.regular }}>{copy.username}</NativeText></TextField.Label>
				{message ? <TextField.SupportingText><NativeText style={{ fontFamily: font.regular }}>{message}</NativeText></TextField.SupportingText> : null}
			</TextField>
			<TextField
				value={passwordText}
				singleLine
				onValueChange={setPassword}
				visualTransformation="password"
				keyboardOptions={{ keyboardType: "password", autoCorrectEnabled: false, capitalization: "none", imeAction: "done" }}
				textStyle={{ fontFamily: font.regular }}
				{...field}
			>
				<TextField.Label><NativeText style={{ fontFamily: font.regular }}>{copy.password}</NativeText></TextField.Label>
			</TextField>
		</Frame>
	);
}

function SelectDialog({ dialog, answer }: { dialog: Extract<BrowserDialog, { kind: "select" }>; answer: Answer }) {
	const t = useTheme();
	return (
		<Frame title={askTitle(dialog)} onDismiss={() => answer(null)} dismiss={<Button label={copy.cancel} onClick={() => answer(null)} />}>
			<LazyColumn modifiers={[fillMaxWidth(), ...(dialog.options.length > 7 ? [height(420)] : [])]}>
				{dialog.options.map((option, index) => (
					<ListItem
						key={index}
						colors={{ containerColor: t.bgElevated, contentColor: index === dialog.selected ? t.accent : t.textPrimary }}
						modifiers={option.disabled ? [alpha(stateOpacity.disabled)] : [clickable(() => answer(answerSelect(dialog, index)))]}
					>
						<ListItem.HeadlineContent>
							<NativeText style={{ typography: "bodyLarge", fontFamily: index === dialog.selected ? font.medium : font.regular }}>{option.label}</NativeText>
						</ListItem.HeadlineContent>
					</ListItem>
				))}
			</LazyColumn>
		</Frame>
	);
}

const styles = StyleSheet.create({
	// The dialog draws in its own window; the host only anchors it.
	host: { position: "absolute" },
});
