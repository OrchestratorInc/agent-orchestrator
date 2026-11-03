import type { SwitchProps } from "@expo/ui";
import { graphicsLayer } from "@expo/ui/jetpack-compose/modifiers";

// Material 3's switch is a fixed 52×32dp with a thick track, so beside a 15sp
// label it read as oversized. Draw it at 0.8 and size the Host to the drawn
// pill, so its trailing edge lines up with the other rows' values and chevrons.
const SCALE = 0.8;

export const settingsSwitchModifiers: NonNullable<SwitchProps["modifiers"]> = [graphicsLayer({ scaleX: SCALE, scaleY: SCALE })];
// Kept 44 tall: the Host bounds are the touch target.
export const settingsSwitchSize = { width: Math.ceil(52 * SCALE), height: 44 };
