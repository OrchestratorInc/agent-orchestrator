import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The over-keyboard window is native (patched into react-native-keyboard-controller).
const patch = readFileSync(new URL("../patches/react-native-keyboard-controller+1.21.9.patch", import.meta.url), "utf8");
const added = (name: string) => {
	const start = patch.indexOf(`+  override fun ${name}(`);
	return start < 0 ? "" : patch.slice(start, patch.indexOf("+  }\n", start));
};

describe("over-keyboard window", () => {
	// A rotation while the overlay is hidden reaches no view: the host view
	// must take the display's size again when the window is shown.
	it("sizes its host view to the display when shown, as well as on a configuration change", () => {
		expect(added("onConfigurationChanged")).toContain("syncHostSize()");
		expect(added("onAttachedToWindow")).toContain("syncHostSize()");
		expect(patch).toContain("+  private fun syncHostSize() {");
		expect(patch).toContain("+    if (params.width == display.x && params.height == display.y) return");
	});
});
