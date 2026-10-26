import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("./composer-glass.ios.tsx", import.meta.url)), "utf8");

describe("iOS composer glass", () => {
	it("fills its pill even when a transform-lifted dock lays out under the keyboard", () => {
		expect(source).toContain('<Host ignoreSafeArea="all" style={StyleSheet.absoluteFill}');
	});
});
