import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const app = JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8")).expo;
const { withPlugins } = createRequire(import.meta.url)("expo/config-plugins");
const projectRoot = fileURLToPath(new URL("..", import.meta.url));

// The iOS 27 SDK rejects the legacy app lifecycle before JavaScript starts.
// Exercise our real build-properties configuration without writing a native
// project: losing its opt-in must stop emitting the required scene lifecycle.
describe("iOS scene lifecycle compatibility", () => {
	function nativeConfig() {
		const plugin = app.plugins.find((entry: unknown) => Array.isArray(entry) && entry[0] === "expo-build-properties");
		return withPlugins({ ...app, _internal: { projectRoot } }, [plugin]);
	}

	it("emits the scene delegate manifest while retaining callback schemes", async () => {
		const config = nativeConfig();
		expect(config.mods.ios.infoPlist).toBeTypeOf("function");
		const callbackSchemes = [{ CFBundleURLSchemes: ["aomobile", "aoagents.ao", "ao-app"] }];
		const result = await config.mods.ios.infoPlist({
			...config,
			modRequest: { projectRoot, platform: "ios", modName: "infoPlist" },
			modResults: { CFBundleURLTypes: callbackSchemes },
		});
		expect(result.modResults.UIApplicationSceneManifest).toEqual({
			UIApplicationSupportsMultipleScenes: false,
			UISceneConfigurations: {
				UIWindowSceneSessionRoleApplication: [{
					UISceneConfigurationName: "Default Configuration",
					UISceneDelegateClassName: "EXExpoAppSceneDelegate",
				}],
			},
		});
		expect(result.modResults.CFBundleURLTypes).toEqual(callbackSchemes);
	});

	it("moves native startup to the scene delegate without removing URL callbacks", async () => {
		const config = nativeConfig();
		const appDelegate = config.mods.ios.appDelegate;
		expect(appDelegate).toBeTypeOf("function");
		const callbacks = "  // Existing application(_:open:options:) and universal-link overrides\n";
		const result = await appDelegate({
			...config,
			modRequest: { projectRoot, platform: "ios", modName: "appDelegate" },
			modResults: { language: "swift", contents: `class AppDelegate: ExpoAppDelegate {
    reactNativeFactory = factory

#if os(iOS) || os(tvOS)
    window = UIWindow(frame: UIScreen.main.bounds)
    factory.startReactNative(
      withModuleName: "main",
      in: window,
      launchOptions: launchOptions)
#endif
${callbacks}}
` },
		});
		expect(result.modResults.contents).toBe(`class AppDelegate: ExpoAppDelegate, ExpoReactNativeFactoryProvider {
    reactNativeFactory = factory

${callbacks}}
`);
	});
});
