const fs = require("node:fs");
const path = require("node:path");
const { withAndroidStyles, withDangerousMod } = require("@expo/config-plugins");

// React Native draws Alert.alert as an AppCompat AlertDialog, and Expo's base
// theme leaves that dialog as AppCompat drew it in 2015: a grey slab with
// square corners and teal, upper-case buttons. This plugin gives it Material
// 3's shape (28dp corners, inset from the screen edges) in the app's own
// surfaces, ink and family, with sentence-case actions, so a confirmation
// looks like the rest of the app.
//
// A native dialog cannot read the JS palette, so it takes the default palette
// for the system's light or dark mode. `ANDROID_DIALOG_COLORS` mirrors
// lib/theme.ts, and a test keeps the two in step.

const FONTS = path.join(__dirname, "..", "assets", "fonts", "dm-sans");

/** Font resources, named as Android requires (lowercase, underscores). */
const FONT_FILES = {
	ao_dm_sans_regular: "DMSans_400Regular.ttf",
	ao_dm_sans_medium: "DMSans_500Medium.ttf",
	ao_dm_sans_semibold: "DMSans_600SemiBold.ttf",
};

const ANDROID_DIALOG_COLORS = {
	// A card on the dark canvas; a sheet's white on the light one.
	light: { surface: "#ffffff", title: "#27272a", message: "#52525c", action: "#27272a" },
	dark: { surface: "#27272a", title: "#f5f5f5", message: "#a1a1a1", action: "#f5f5f5" },
};

/** The text cursor and selection handles in RN fields: the palette's highlight. */
const CONTROL_ACCENT = { light: "#1b4ed8", dark: "#4b7cf3" };

function colorsXml(scheme) {
	const c = ANDROID_DIALOG_COLORS[scheme];
	return `<?xml version="1.0" encoding="utf-8"?>
<resources>
  <color name="ao_dialog_surface">${c.surface}</color>
  <color name="ao_dialog_title">${c.title}</color>
  <color name="ao_dialog_message">${c.message}</color>
  <color name="ao_dialog_action">${c.action}</color>
  <color name="ao_control_accent">${CONTROL_ACCENT[scheme]}</color>
</resources>
`;
}

const BACKGROUND_XML = `<?xml version="1.0" encoding="utf-8"?>
<inset xmlns:android="http://schemas.android.com/apk/res/android"
    android:insetLeft="24dp"
    android:insetRight="24dp"
    android:insetTop="24dp"
    android:insetBottom="24dp">
  <shape android:shape="rectangle">
    <corners android:radius="28dp" />
    <solid android:color="@color/ao_dialog_surface" />
  </shape>
</inset>
`;

const STYLES_XML = `<?xml version="1.0" encoding="utf-8"?>
<resources>
  <style name="AoAlertDialog" parent="Theme.AppCompat.DayNight.Dialog.Alert">
    <item name="android:windowBackground">@drawable/ao_dialog_background</item>
    <item name="android:windowMinWidthMajor">@dimen/abc_dialog_min_width_major</item>
    <item name="android:windowMinWidthMinor">@dimen/abc_dialog_min_width_minor</item>
    <item name="android:backgroundDimAmount">0.56</item>
    <item name="android:textColorPrimary">@color/ao_dialog_title</item>
    <item name="android:textColorSecondary">@color/ao_dialog_message</item>
    <item name="android:textColorAlertDialogListItem">@color/ao_dialog_title</item>
    <item name="android:fontFamily">@font/ao_dm_sans_regular</item>
    <item name="fontFamily">@font/ao_dm_sans_regular</item>
    <item name="colorAccent">@color/ao_dialog_action</item>
    <item name="android:windowTitleStyle">@style/AoAlertDialog.Title</item>
    <item name="buttonBarButtonStyle">@style/AoAlertDialog.Action</item>
    <item name="buttonBarPositiveButtonStyle">@style/AoAlertDialog.Action</item>
    <item name="buttonBarNegativeButtonStyle">@style/AoAlertDialog.Action</item>
    <item name="buttonBarNeutralButtonStyle">@style/AoAlertDialog.Action</item>
  </style>
  <style name="AoAlertDialog.Title" parent="RtlOverlay.DialogWindowTitle.AppCompat">
    <item name="android:textAppearance">@style/AoAlertDialog.TitleText</item>
    <!-- On the view as well: AppCompat's title keeps the platform face from
         its own appearance otherwise. -->
    <item name="android:fontFamily">@font/ao_dm_sans_semibold</item>
    <item name="fontFamily">@font/ao_dm_sans_semibold</item>
    <item name="android:textSize">20sp</item>
    <item name="android:textColor">@color/ao_dialog_title</item>
  </style>
  <style name="AoAlertDialog.TitleText" parent="TextAppearance.AppCompat.Title">
    <item name="android:textSize">20sp</item>
    <item name="android:fontFamily">@font/ao_dm_sans_semibold</item>
    <item name="fontFamily">@font/ao_dm_sans_semibold</item>
    <item name="android:textColor">@color/ao_dialog_title</item>
  </style>
  <style name="AoAlertDialog.Action" parent="Widget.AppCompat.Button.ButtonBar.AlertDialog">
    <item name="android:textAllCaps">false</item>
    <item name="textAllCaps">false</item>
    <item name="android:textSize">15sp</item>
    <item name="android:fontFamily">@font/ao_dm_sans_semibold</item>
    <item name="fontFamily">@font/ao_dm_sans_semibold</item>
    <item name="android:textColor">@color/ao_dialog_action</item>
    <item name="android:minWidth">64dp</item>
    <item name="android:paddingStart">16dp</item>
    <item name="android:paddingEnd">16dp</item>
  </style>
</resources>
`;

function writeDialogResources(platformProjectRoot) {
	const res = path.join(platformProjectRoot, "app", "src", "main", "res");
	const write = (dir, name, contents) => {
		fs.mkdirSync(path.join(res, dir), { recursive: true });
		fs.writeFileSync(path.join(res, dir, name), contents);
	};
	fs.mkdirSync(path.join(res, "font"), { recursive: true });
	for (const [name, file] of Object.entries(FONT_FILES)) {
		fs.copyFileSync(path.join(FONTS, file), path.join(res, "font", `${name}.ttf`));
	}
	write("values", "ao_dialog_colors.xml", colorsXml("light"));
	write("values-night", "ao_dialog_colors.xml", colorsXml("dark"));
	write("drawable", "ao_dialog_background.xml", BACKGROUND_XML);
	write("values", "ao_dialog_styles.xml", STYLES_XML);
}

/** Points the app theme's dialogs at AoAlertDialog and its controls at the highlight. */
function applyAppTheme(styles) {
	const appTheme = styles.resources.style?.find((style) => style.$.name === "AppTheme");
	if (!appTheme) return styles;
	const items = [
		["alertDialogTheme", "@style/AoAlertDialog"],
		["android:alertDialogTheme", "@style/AoAlertDialog"],
		// React Native builds the dialog's title itself (for its accessibility
		// heading) and inflates it against the activity, so the title's style
		// is read from this theme, not the dialog's. The activity has no title
		// bar, so nothing else reads it.
		["android:windowTitleStyle", "@style/AoAlertDialog.Title"],
		["colorAccent", "@color/ao_control_accent"],
	];
	appTheme.item = (appTheme.item ?? []).filter((item) => !items.some(([name]) => name === item.$.name));
	for (const [name, value] of items) appTheme.item.push({ $: { name }, _: value });
	return styles;
}

function withAndroidDialogTheme(config) {
	config = withAndroidStyles(config, (config) => {
		config.modResults = applyAppTheme(config.modResults);
		return config;
	});
	return withDangerousMod(config, ["android", (config) => {
		writeDialogResources(config.modRequest.platformProjectRoot);
		return config;
	}]);
}

module.exports = withAndroidDialogTheme;
module.exports.ANDROID_DIALOG_COLORS = ANDROID_DIALOG_COLORS;
module.exports.CONTROL_ACCENT = CONTROL_ACCENT;
module.exports.applyAppTheme = applyAppTheme;
