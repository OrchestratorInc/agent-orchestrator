import type { SwitchProps } from "@expo/ui";
import { Platform } from "react-native";
import { labelsHidden, scaleEffect } from "@expo/ui/swift-ui/modifiers";

// The native switch keeps its fixed size whatever the row's type scale is,
// and iOS 26 widened it to 63pt, past the old 54pt Host, so it hung over the
// row's trailing edge and read as oversized beside a 15pt label. Draw it at
// 0.8 around its centre. labelsHidden drops the spacing an empty label still
// reserves (~8pt, which pushed the pill past the Host's trailing edge), so the
// toggle is exactly the pill and fits a Host of the native width. A negative
// trailing margin takes back the space the scale leaves, so the pill's
// trailing edge lines up with the other rows' values.
const SCALE = 0.8;
const nativeWidth = Number.parseInt(String(Platform.Version), 10) >= 26 ? 63 : 51;

export const settingsSwitchModifiers: NonNullable<SwitchProps["modifiers"]> = [labelsHidden(), scaleEffect(SCALE)];
// Kept 44 tall: the Host bounds are the touch target.
export const settingsSwitchSize = { width: nativeWidth, height: 44, marginRight: -(nativeWidth * (1 - SCALE)) / 2 };
