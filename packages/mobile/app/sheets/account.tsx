import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { Platform } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { readChatSheet, releaseChatSheet } from "../../lib/chat/chatSheetRegistry";
import { useSheetEntryPresent } from "../../lib/chat/useSheetEntryPresent";
import { haptics } from "../../lib/haptics";
import { NativePickerList } from "../../lib/NativePickerList";
import { SheetHeader } from "../../lib/ui";

export default function AccountSheetRoute() {
	const router = useRouter();
	const { sheetKey } = useLocalSearchParams<{ sheetKey?: string }>();
	const entry = readChatSheet(sheetKey);
	const picker = entry?.kind === "account-picker" ? entry : undefined;
	// Follows the accounts while it is up: usage read again, or an account signed out.
	const [state, setState] = useState(picker ? { options: picker.options, selected: picker.selected } : undefined);
	useEffect(() => picker?.subscribe?.(setState), [picker]);
	useEffect(() => () => releaseChatSheet(sheetKey), [sheetKey]);
	useSheetEntryPresent(entry?.kind === "account-picker");
	if (entry?.kind !== "account-picker") return null;
	return (
		<NativePickerList
			header={<SheetHeader title={Platform.OS === "ios" ? undefined : "Account"} subtitle={entry.subtitle} />}
			options={state?.options ?? entry.options}
			selected={state?.selected ?? entry.selected}
			onSelect={(id) => {
				haptics.select();
				backOr(router);
				if (id !== (state?.selected ?? entry.selected)) entry.onSelect(id);
			}}
			refreshing={false}
			onRefresh={async () => {}}
			empty="No accounts on this machine."
		/>
	);
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
