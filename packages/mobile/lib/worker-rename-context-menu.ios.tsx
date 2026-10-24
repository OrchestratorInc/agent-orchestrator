import { Host } from "@expo/ui";
import { Button, ContextMenu, RNHostView } from "@expo/ui/swift-ui";
import { type ReactNode } from "react";
import { Pressable } from "react-native";
import type { GestureType } from "react-native-gesture-handler";
import type { MutableRefObject } from "react";
import { workerActionSymbol, type WorkerAction, type WorkerActionId } from "./worker-action-model";

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
	return (
		// Community MenuView fits both axes to the trigger's intrinsic size.
		// A list row must instead inherit its list width at BOTH hosting boundaries;
		// only its height follows the RN content.
		<Host matchContents={{ vertical: true }} style={{ width: "100%" }} testID="worker-row-menu" ignoreSafeArea="all">
			<ContextMenu>
				<ContextMenu.Trigger>
					<RNHostView matchContents={{ vertical: true }}>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel={accessibilityLabel}
							accessibilityHint={accessibilityHint}
							onPress={onPress}
							style={({ pressed }) => [style, pressed && pressedStyle]}
						>
							{children}
						</Pressable>
					</RNHostView>
				</ContextMenu.Trigger>
				<ContextMenu.Items>
					{actions.map((action) => (
						<Button
							key={action.id}
							label={action.title}
							systemImage={workerActionSymbol(action.id)}
							role={action.destructive ? "destructive" : undefined}
							onPress={() => onAction(action.id)}
						/>
					))}
				</ContextMenu.Items>
			</ContextMenu>
		</Host>
	);
}
