import AsyncStorage from "@react-native-async-storage/async-storage";
import { useEffect, useState } from "react";
import type { ChatConfigOption } from "./types";

/*
 * The settings an agent last reported in a chat on this machine (its own
 * permission modes, by its own names), kept so New task draws its controls
 * from the same source as the chat before any session of its own exists:
 * desktop's New chat does the same from its chat settings template. Without
 * it New task named Claude's modes in AO's generic words ("Use agent
 * permissions") where the chat said "Manual".
 */

const templateKey = (machine: string, harness: string) => `ao.chatSettings.${machine}.${harness}`;

/** Keeps the settings `harness` reported in a chat on `machine`. */
export async function rememberChatSettings(machine: string, harness: string, options: readonly ChatConfigOption[]): Promise<void> {
	if (!options.length) return;
	await AsyncStorage.setItem(templateKey(machine, harness), JSON.stringify(options)).catch(() => {});
}

/** The settings `harness` last reported in a chat on `machine`; none before one has. */
export function useChatSettingsTemplate(machine: string | null | undefined, harness: string): ChatConfigOption[] | undefined {
	const [options, setOptions] = useState<ChatConfigOption[]>();
	useEffect(() => {
		setOptions(undefined);
		if (!machine || !harness) return;
		let live = true;
		AsyncStorage.getItem(templateKey(machine, harness))
			.then((raw) => {
				if (!live || !raw) return;
				const parsed: unknown = JSON.parse(raw);
				if (Array.isArray(parsed)) setOptions(parsed as ChatConfigOption[]);
			})
			.catch(() => {});
		return () => { live = false; };
	}, [machine, harness]);
	return options;
}
