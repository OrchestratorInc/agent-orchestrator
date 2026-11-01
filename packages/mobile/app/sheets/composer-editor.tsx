import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Platform } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { ComposerEditor } from "../../lib/chat/ComposerEditor";
import { readChatSheet, releaseChatSheet } from "../../lib/chat/chatSheetRegistry";
import { useSheetEntryPresent } from "../../lib/chat/useSheetEntryPresent";
import { symbols } from "../../lib/symbols";
import { toolbarIcon } from "../../lib/toolbarIcon";

/**
 * The chat composer's draft, opened full screen from its expand button. Modal,
 * so iOS swipes it away like any page sheet, and every keystroke is written
 * back to the composer, so closing it any way keeps the draft.
 *
 * iOS: the bar collapses the editor at the leading edge and sends at the
 * trailing one, as a compose sheet does. Android: the top app bar's own back
 * collapses it, and Send is its action.
 */
export default function ComposerEditorRoute() {
	const router = useRouter();
	const { sheetKey } = useLocalSearchParams<{ sheetKey?: string }>();
	const entry = readChatSheet(sheetKey);
	const editor = entry?.kind === "composer-editor" ? entry : undefined;
	const [text, setText] = useState(editor?.text ?? "");
	useEffect(() => () => releaseChatSheet(sheetKey), [sheetKey]);
	useSheetEntryPresent(Boolean(editor));
	const send = () => {
		backOr(router);
		editor?.onSend();
	};
	const sendRef = useRef(send);
	sendRef.current = send;
	const canSend = Boolean(text.trim());
	const sendLabel = editor?.sendLabel ?? "Send";
	// Memoized on what the bar shows, so typing doesn't register the header
	// again on every key; the item sends the latest draft.
	const headerItems = useMemo(() => (
		<>
			{Platform.OS === "ios" ? (
				<Stack.Toolbar placement="left">
					<Stack.Toolbar.Button icon={toolbarIcon({ ios: symbols.collapseEditor.ios, android: "close" })} accessibilityLabel="Close full-screen editor" onPress={() => backOr(router)} />
				</Stack.Toolbar>
			) : null}
			<Stack.Toolbar placement="right">
				{Platform.OS === "ios" ? (
					<Stack.Toolbar.Button accessibilityLabel={`${sendLabel} message`} disabled={!canSend} style={{ fontWeight: "600" }} onPress={() => sendRef.current()}>{sendLabel}</Stack.Toolbar.Button>
				) : (
					<Stack.Toolbar.Button icon={toolbarIcon("send")} accessibilityLabel={`${sendLabel} message`} disabled={!canSend} onPress={() => sendRef.current()} />
				)}
			</Stack.Toolbar>
		</>
	), [canSend, router, sendLabel]);
	if (!editor) return null;
	return (
		<>
			{headerItems}
			<ComposerEditor
				value={text}
				placeholder={editor.placeholder}
				onChangeText={(value) => {
					setText(value);
					editor.onChange(value);
				}}
			/>
		</>
	);
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
