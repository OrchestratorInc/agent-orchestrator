const { withAppDelegate, withInfoPlist } = require("@expo/config-plugins");

// iOS 27 traps at launch (_UIApplicationEvaluateRuntimeIssueForNoSceneLifecycleAdoption)
// unless the app adopts the scene life cycle. Expo ships the scene delegate
// (EXExpoAppSceneDelegate), but its template still starts React Native from
// the app delegate. This plugin names the scene delegate in Info.plist and
// leaves the window to it: the app delegate keeps creating the React Native
// factory and hands it over through ExpoReactNativeFactoryProvider.

const SCENE_MANIFEST = {
	UIApplicationSupportsMultipleScenes: false,
	UISceneConfigurations: {
		UIWindowSceneSessionRoleApplication: [
			{
				UISceneConfigurationName: "Default Configuration",
				UISceneDelegateClassName: "EXExpoAppSceneDelegate",
			},
		],
	},
};

const CLASS_DECLARATION = "class AppDelegate: ExpoAppDelegate {";
const PROVIDER_DECLARATION = "class AppDelegate: ExpoAppDelegate, ExpoReactNativeFactoryProvider {";

// The template's window setup; the scene delegate does this once a scene connects.
const START_IN_APP_DELEGATE = /\n#if os\(iOS\) \|\| os\(tvOS\)\n\s*window = UIWindow\(frame: UIScreen\.main\.bounds\)\n\s*factory\.startReactNative\([\s\S]*?\)\n#endif\n/;

function adoptSceneLifecycle(contents) {
	if (contents.includes(PROVIDER_DECLARATION)) return contents;
	if (!contents.includes(CLASS_DECLARATION) || !START_IN_APP_DELEGATE.test(contents)) {
		throw new Error("withIosSceneLifecycle: AppDelegate.swift no longer matches Expo's template; update the plugin.");
	}
	return contents.replace(CLASS_DECLARATION, PROVIDER_DECLARATION).replace(START_IN_APP_DELEGATE, "");
}

function withIosSceneLifecycle(config) {
	config = withInfoPlist(config, (config) => {
		config.modResults.UIApplicationSceneManifest = SCENE_MANIFEST;
		return config;
	});
	return withAppDelegate(config, (config) => {
		if (config.modResults.language !== "swift") {
			throw new Error("withIosSceneLifecycle: expected a Swift AppDelegate.");
		}
		config.modResults.contents = adoptSceneLifecycle(config.modResults.contents);
		return config;
	});
}

module.exports = withIosSceneLifecycle;
module.exports.adoptSceneLifecycle = adoptSceneLifecycle;
module.exports.SCENE_MANIFEST = SCENE_MANIFEST;
