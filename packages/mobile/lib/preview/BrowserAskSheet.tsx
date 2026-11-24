import { Host } from "@expo/ui";
import { Alert, Button, ConfirmationDialog, SecureField, Spacer, Text, TextField, useNativeState } from "@expo/ui/swift-ui";
import { autocorrectionDisabled, frame, textInputAutocapitalization } from "@expo/ui/swift-ui/modifiers";
import { useRef, useState, type ReactNode } from "react";
import { StyleSheet } from "react-native";
import { useThemeState } from "../ThemeProvider";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";
import { answerAuth, answerDialog, answerSelect, askTitle, authMessage } from "./browserAsk";
import { browserAskCopy as copy, type BrowserAskSheetProps } from "./BrowserAskSheet.types";
import { answerWithFiles, fileSources, filesAnswer } from "./browserAskAlerts";

export type { BrowserAskSheetProps } from "./BrowserAskSheet.types";

type Answer = (command: BrowserCommand | null) => void;

/**
 * What the page asks, as SwiftUI's own alert and confirmation dialog. Both
 * are presented only while this renders them, so a dialog the host closed,
 * replaced or that the screen left goes away with it. A sign-in's password
 * field is the system's secure field.
 */
export function BrowserAskSheet({ dialog, onAnswer, pickers }: BrowserAskSheetProps) {
	const latest = useRef({ onAnswer, pickers });
	latest.current = { onAnswer, pickers };
	const answered = useRef<BrowserDialog | null>(null);
	const [, closed] = useState(0);
	const answer = (asked: BrowserDialog): Answer => (command) => {
		if (answered.current === asked) return;
		answered.current = asked;
		closed((count) => count + 1);
		latest.current.onAnswer(asked, command);
	};

	if (!dialog || answered.current === dialog) return null;
	const respond = answer(dialog);
	return <AskHost><Ask key={keyOf(dialog)} dialog={dialog} answer={respond} pickers={() => latest.current.pickers} /></AskHost>;
}

/** SwiftUI presents an alert from a view, so the host is a one-point anchor. */
function AskHost({ children }: { children: ReactNode }) {
	const { scheme } = useThemeState();
	return (
		<Host style={styles.anchor} colorScheme={scheme}>
			{children}
		</Host>
	);
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

function Ask({ dialog, answer, pickers }: { dialog: BrowserDialog; answer: Answer; pickers: () => BrowserAskSheetProps["pickers"] }) {
	switch (dialog.kind) {
		case "alert":
			return <PageAlert title={askTitle(dialog)} message={dialog.message} onDismiss={() => answer(answerDialog(true))}>
				<Button label={copy.ok} onPress={() => answer(answerDialog(true))} />
			</PageAlert>;
		case "confirm":
			return <PageAlert title={askTitle(dialog)} message={dialog.message} onDismiss={() => answer(answerDialog(false))}>
				<Button label={copy.cancel} role="cancel" onPress={() => answer(answerDialog(false))} />
				<Button label={copy.ok} onPress={() => answer(answerDialog(true))} />
			</PageAlert>;
		case "beforeunload":
			return <PageAlert title={askTitle(dialog)} message={copy.leaveMessage} onDismiss={() => answer(answerDialog(false))}>
				<Button label={copy.stay} role="cancel" onPress={() => answer(answerDialog(false))} />
				<Button label={copy.leave} role="destructive" onPress={() => answer(answerDialog(true))} />
			</PageAlert>;
		case "prompt":
			return <PromptAlert dialog={dialog} answer={answer} />;
		case "auth":
			return <AuthAlert dialog={dialog} answer={answer} />;
		case "select":
			return (
				<Choices title={askTitle(dialog)} onDismiss={() => answer(null)}>
					{dialog.options.flatMap((option, index) => (option.disabled ? [] : [
						<Button key={index} label={index === dialog.selected ? `✓ ${option.label}` : option.label} onPress={() => answer(answerSelect(dialog, index))} />,
					]))}
					<Button label={copy.cancel} role="cancel" onPress={() => answer(null)} />
				</Choices>
			);
		case "filechooser":
			return <FileSourceChoices dialog={dialog} answer={answer} pickers={pickers} />;
	}
}

function FileSourceChoices({ dialog, answer, pickers }: { dialog: Extract<BrowserDialog, { kind: "filechooser" }>; answer: Answer; pickers: () => BrowserAskSheetProps["pickers"] }) {
	// Choosing a source dismisses the dialog too, while its picker is still
	// open: only a dismissal with no source chosen cancels the input.
	const chosen = useRef(false);
	const none = () => { if (!chosen.current) answer(filesAnswer(dialog, [])); };
	return (
		<Choices title={askTitle(dialog)} onDismiss={none}>
			{fileSources(dialog).map((source) => (
				<Button
					key={source}
					label={source === "photos" ? copy.photos : copy.files(dialog.multiple)}
					onPress={() => {
						chosen.current = true;
						void answerWithFiles(source, dialog, answer, pickers());
					}}
				/>
			))}
			<Button label={copy.cancel} role="cancel" onPress={() => answer(filesAnswer(dialog, []))} />
		</Choices>
	);
}

/** Presented while mounted; dismissing it any other way is `onDismiss`. */
function PageAlert({ title, message, onDismiss, children }: { title: string; message?: string; onDismiss(): void; children: ReactNode }) {
	return (
		<Alert title={title} isPresented onIsPresentedChange={(shown) => { if (!shown) onDismiss(); }}>
			<Alert.Trigger><Spacer modifiers={[frame({ width: 1, height: 1 })]} /></Alert.Trigger>
			<Alert.Actions>{children}</Alert.Actions>
			{message ? <Alert.Message><Text>{message}</Text></Alert.Message> : null}
		</Alert>
	);
}

function Choices({ title, onDismiss, children }: { title: string; onDismiss(): void; children: ReactNode }) {
	return (
		<ConfirmationDialog title={title} titleVisibility="visible" isPresented onIsPresentedChange={(shown) => { if (!shown) onDismiss(); }}>
			<ConfirmationDialog.Trigger><Spacer modifiers={[frame({ width: 1, height: 1 })]} /></ConfirmationDialog.Trigger>
			<ConfirmationDialog.Actions>{children}</ConfirmationDialog.Actions>
		</ConfirmationDialog>
	);
}

function PromptAlert({ dialog, answer }: { dialog: Extract<BrowserDialog, { kind: "prompt" }>; answer: Answer }) {
	const text = useNativeState(dialog.defaultPrompt ?? "");
	const value = useRef(dialog.defaultPrompt ?? "");
	return (
		<PageAlert title={askTitle(dialog)} message={dialog.message} onDismiss={() => answer(answerDialog(false))}>
			<TextField text={text} onTextChange={(next) => { value.current = next; }} />
			<Button label={copy.cancel} role="cancel" onPress={() => answer(answerDialog(false))} />
			<Button label={copy.ok} onPress={() => answer(answerDialog(true, value.current))} />
		</PageAlert>
	);
}

function AuthAlert({ dialog, answer }: { dialog: Extract<BrowserDialog, { kind: "auth" }>; answer: Answer }) {
	const usernameText = useNativeState("");
	const passwordText = useNativeState("");
	// Held only until the alert answers: never stored, logged or shown.
	const typed = useRef({ username: "", password: "" });
	return (
		<PageAlert title={askTitle(dialog)} message={authMessage(dialog)} onDismiss={() => answer(answerAuth(dialog, null))}>
			<TextField text={usernameText} placeholder={copy.username} modifiers={[autocorrectionDisabled(true), textInputAutocapitalization("never")]} onTextChange={(next) => { typed.current.username = next; }} />
			<SecureField text={passwordText} placeholder={copy.password} onTextChange={(next) => { typed.current.password = next; }} />
			<Button label={copy.cancel} role="cancel" onPress={() => answer(answerAuth(dialog, null))} />
			<Button label={copy.signIn} onPress={() => answer(answerAuth(dialog, typed.current))} />
		</PageAlert>
	);
}

const styles = StyleSheet.create({
	// The alert draws in its own window; the host only anchors it.
	anchor: { position: "absolute", top: 0, left: 0, width: 1, height: 1 },
});
