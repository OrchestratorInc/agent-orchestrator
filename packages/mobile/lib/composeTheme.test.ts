import { describe, expect, it } from "vitest";

import { darkTheme, lightTheme } from "./theme";

// The plugin is CommonJS run by the Expo CLI; vitest loads it as-is.
// eslint-disable-next-line @typescript-eslint/no-require-imports
const dialogPlugin = require("../plugins/withAndroidDialogTheme") as {
	ANDROID_DIALOG_COLORS: Record<"light" | "dark", { surface: string; title: string; message: string; action: string }>;
	CONTROL_ACCENT: Record<"light" | "dark", string>;
	applyAppTheme(styles: { resources: { style?: { $: { name: string }; item?: { $: { name: string }; _: string }[] }[] } }): unknown;
};

describe("Android dialog theme plugin", () => {
	// A native dialog cannot read the JS palette, so the plugin copies it.
	it("matches the default palette in both schemes", () => {
		const { ANDROID_DIALOG_COLORS: colors, CONTROL_ACCENT: accent } = dialogPlugin;
		expect(colors.dark).toEqual({ surface: darkTheme.bgElevated, title: darkTheme.textPrimary, message: darkTheme.textSecondary, action: darkTheme.accent });
		expect(colors.light).toEqual({ surface: lightTheme.bgSurface, title: lightTheme.textPrimary, message: lightTheme.textSecondary, action: lightTheme.accent });
		expect(accent).toEqual({ light: lightTheme.highlight, dark: darkTheme.highlight });
	});

	it("points AppTheme's dialogs at the app's dialog theme once, however often prebuild runs", () => {
		const styles = { resources: { style: [{ $: { name: "AppTheme" }, item: [{ $: { name: "colorPrimary" }, _: "@color/colorPrimary" }] }] } };
		dialogPlugin.applyAppTheme(styles);
		dialogPlugin.applyAppTheme(styles);
		const items = styles.resources.style[0].item.map((item) => [item.$.name, item._]);
		expect(items).toEqual([
			["colorPrimary", "@color/colorPrimary"],
			["alertDialogTheme", "@style/AoAlertDialog"],
			["android:alertDialogTheme", "@style/AoAlertDialog"],
			["android:windowTitleStyle", "@style/AoAlertDialog.Title"],
			["colorAccent", "@color/ao_control_accent"],
		]);
	});
});
