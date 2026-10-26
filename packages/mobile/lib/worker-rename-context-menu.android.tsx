import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { type MutableRefObject, type ReactNode } from "react";
import { Pressable, type ImageSourcePropType } from "react-native";
import type { GestureType } from "react-native-gesture-handler";
import { useTheme, useThemeState } from "./ThemeProvider";
import { workerActionSymbols, type WorkerAction, type WorkerActionDrawable, type WorkerActionId } from "./worker-action-model";

// Android's menu icons are bundled drawables, not resolved by name: each is the
// action's Material Symbol, written by scripts/generate-material-drawables.mjs.
// A `require` of a missing file fails at bundle time rather than rendering
// blank, and the type keeps this map complete for every action.
const DRAWABLES: Record<WorkerActionDrawable, ImageSourcePropType> = {
	arrow_forward: require("../assets/icons/arrow_forward.xml"),
	keep: require("../assets/icons/keep.xml"),
	keep_off: require("../assets/icons/keep_off.xml"),
	edit: require("../assets/icons/edit.xml"),
	refresh: require("../assets/icons/refresh.xml"),
	open_in_new: require("../assets/icons/open_in_new.xml"),
	archive: require("../assets/icons/archive.xml"),
};

// MenuView owns Android's long-press natively. It must remain the trigger while
// the row is nested in Swipeable; React Native and Gesture Handler callbacks
// lose that touch race to the swipe container.
export function WorkerRowContextMenu({
	children,
	onPress,
	actions,
	onAction,
	gestureRef: _gestureRef,
	accessibilityLabel,
	accessibilityHint,
	style,
	pressedStyle,
}: {
	children: ReactNode;
	onPress(): void;
	actions: WorkerAction[];
	onAction(id: WorkerActionId): void;
	gestureRef: MutableRefObject<GestureType | undefined>;
	accessibilityLabel: string;
	accessibilityHint: string;
	style: object;
	pressedStyle: object;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();

	const menuActions: MenuAction[] = actions.map((action) => ({
		id: action.id,
		title: action.title,
		image: DRAWABLES[workerActionSymbols[action.id].android],
		imageColor: action.destructive ? t.red : t.textSecondary,
		titleColor: action.destructive ? t.red : t.textPrimary,
		attributes: action.destructive ? { destructive: true } : undefined,
	}));

	const chooseAction = (event: NativeActionEvent) => {
		onAction(event.nativeEvent.event as WorkerActionId);
	};

	return (
		<MenuView
			colorScheme={scheme}
			actions={menuActions}
			shouldOpenOnLongPress
			onPressAction={chooseAction}
			testID="worker-row-menu"
		>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={accessibilityLabel}
				accessibilityHint={accessibilityHint}
				onPress={onPress}
				style={({ pressed }) => [style, pressed && pressedStyle]}
			>
				{children}
			</Pressable>
		</MenuView>
	);
}
