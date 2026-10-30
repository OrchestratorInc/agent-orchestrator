import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
const page = source("../app/pair.tsx");

describe("pair screen", () => {
	// A modal closes; it does not go back.
	it("closes from the native bar on iOS and from Material's close button on Android", () => {
		expect(source("../app/_layout.tsx")).toContain('<Stack.Screen name="pair" options={{ presentation: "modal", headerShown: Platform.OS === "ios", title: "Pair a machine" }} />');
		expect(page).toContain('const NATIVE_BAR = Platform.OS === "ios";');
		expect(page).toContain('<Stack.Toolbar.Button icon={toolbarIcon("close")} accessibilityLabel="Close" onPress={() => backOr(router)} />');
		expect(page).toContain('{NATIVE_BAR ? null : <View style={styles.topBar}><NativeHeaderButton icon="close" label="Close" onPress={back} /></View>}');
		expect(page).not.toMatch(/icon="back"|chevron/);
	});

	// One name for the task: the reconnect flow's bar names the machine, and
	// the body repeats it only on Android, which has no bar.
	it("titles a reconnect after the machine instead of as a new pairing", () => {
		expect(page).toContain('const title = repairHostId ? (repairHost ? `Reconnect ${repairHost.name}` : "Reconnect machine") : "Pair a machine";');
		expect(page).toContain("useLayoutEffect(() => navigation.setOptions({ title }), [navigation, title]);");
		expect(page).toContain('{NATIVE_BAR ? null : <Text accessibilityRole="header" style={styles.repairTitle}>Reconnect {repairHost.name}</Text>}');
	});

	it("frames the code with one reticle in the on-camera colours", () => {
		expect(page).toContain("<View style={styles.reticle} />");
		expect(page).toContain("borderColor: onCamera.reticle");
		expect(page).toContain("viewfinderLive: { backgroundColor: onCamera.backdrop }");
		expect(page).not.toMatch(/Corner|rgba\(|#[0-9a-f]{3,8}\b/i);
	});

	// Text actions are the shared Button: a 44/48 target, a ripple on Android
	// and a fade on iOS, where the hand-rolled Pressables had about 34pt and
	// no pressed state at all.
	it("draws its text actions as platform buttons with system symbols", () => {
		expect(page).toContain('<Button variant="ghost" icon="refresh" title="Try again" onPress={retry} />');
		expect(page).toContain('title="Enter details manually"');
		expect(page).not.toMatch(/<Pressable\b|Feather|NoticeAction/);
	});
});
