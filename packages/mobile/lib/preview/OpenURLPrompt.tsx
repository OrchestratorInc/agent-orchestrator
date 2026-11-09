import { useEffect, useRef } from "react";
import { Alert } from "react-native";
import { openURLPromptCopy, type OpenURLPromptProps } from "./OpenURLPrompt.types";

export type { OpenURLPromptProps } from "./OpenURLPrompt.types";

/**
 * "Open URL…" as the system's own text prompt: a UIAlertController with one
 * field and the URL keyboard. It draws nothing in the screen itself.
 */
export function OpenURLPrompt({ visible, onSubmit, onDismiss }: OpenURLPromptProps) {
	// The alert outlives the render that opened it, so its buttons call the
	// latest handlers rather than the ones it was shown with.
	const latest = useRef({ onSubmit, onDismiss });
	latest.current = { onSubmit, onDismiss };

	useEffect(() => {
		if (!visible) return;
		Alert.prompt(
			openURLPromptCopy.title,
			openURLPromptCopy.message,
			[
				{ text: openURLPromptCopy.cancel, style: "cancel", onPress: () => latest.current.onDismiss() },
				{
					text: openURLPromptCopy.open,
					isPreferred: true,
					onPress: (value?: string) => {
						latest.current.onDismiss();
						const url = value?.trim();
						if (url) latest.current.onSubmit(url);
					},
				},
			],
			"plain-text",
			"",
			"url",
		);
	}, [visible]);

	return null;
}
