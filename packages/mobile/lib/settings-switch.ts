import type { SwitchProps } from "@expo/ui";

// A Settings row's native switch, drawn smaller than the platform default.
// The platform files apply the scale; elsewhere the switch keeps its own size.
export const settingsSwitchModifiers: NonNullable<SwitchProps["modifiers"]> = [];
export const settingsSwitchSize = { width: 54, height: 34 };
