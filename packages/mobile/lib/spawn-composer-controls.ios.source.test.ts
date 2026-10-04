import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("./spawn-composer-controls.ios.tsx", import.meta.url)), "utf8");

describe("iOS spawn menu layout", () => {
	it("shares one selector row while keeping the harness and model controls intact", () => {
		expect(source).toContain("const HARNESS_MENU_WIDTH = 124");
		expect(source).toContain('frame({ width: HARNESS_MENU_WIDTH })');
		expect(source).toContain('<Host style={styles.destinationHost}');
		expect(source).toContain('<Host style={styles.projectHost}');
		expect(source).toContain('{projectLabel}</Text>');
		expect(source).toContain('layoutPriority(1)');
		expect(source).toContain('accessibilityIdentifier("spawn-project")');
		expect(source).toContain('accessibilityIdentifier("spawn-model")');
	});
});
