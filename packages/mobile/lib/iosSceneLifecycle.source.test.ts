import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

const require = createRequire(import.meta.url);
const { adoptSceneLifecycle, SCENE_MANIFEST } = require("../plugins/withIosSceneLifecycle") as {
	adoptSceneLifecycle: (contents: string) => string;
	SCENE_MANIFEST: { UISceneConfigurations: { UIWindowSceneSessionRoleApplication: { UISceneDelegateClassName: string }[] } };
};

// Expo's AppDelegate template, as prebuild writes it.
const TEMPLATE = `@main
class AppDelegate: ExpoAppDelegate {
  var window: UIWindow?

  var reactNativeFactory: RCTReactNativeFactory?

  public override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
  ) -> Bool {
    reactNativeFactory = factory

#if os(iOS) || os(tvOS)
    window = UIWindow(frame: UIScreen.main.bounds)
    factory.startReactNative(
      withModuleName: "main",
      in: window,
      launchOptions: launchOptions)
#endif

    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }
}
`;

// iOS 27 traps at launch unless the app adopts the scene life cycle.
describe("iOS scene life cycle", () => {
	it("hands the window to Expo's scene delegate", () => {
		const out = adoptSceneLifecycle(TEMPLATE);
		expect(out).toContain("class AppDelegate: ExpoAppDelegate, ExpoReactNativeFactoryProvider {");
		expect(out).not.toContain("startReactNative");
		expect(out).toContain("reactNativeFactory = factory\n\n    return super.application");
		expect(adoptSceneLifecycle(out)).toBe(out);
	});

	it("fails prebuild when the template changes rather than shipping a window-less app", () => {
		expect(() => adoptSceneLifecycle("class AppDelegate: ExpoAppDelegate {\n}\n")).toThrow(/no longer matches/);
	});

	it("names Expo's scene delegate and runs on prebuild", () => {
		expect(SCENE_MANIFEST.UISceneConfigurations.UIWindowSceneSessionRoleApplication[0].UISceneDelegateClassName).toBe("EXExpoAppSceneDelegate");
		const app = JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8")) as { expo: { plugins: unknown[] } };
		expect(app.expo.plugins).toContain("./plugins/withIosSceneLifecycle");
	});
});
