import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("./ChatTurnSettingsControl.ios.tsx", import.meta.url)), "utf8");

describe("iOS chat controls row", () => {
	// The new task's row, from the same pieces: the model's capsule, the effort's
	// speedometer and the approval mode's symbol.
	it("draws the new task's model capsule, effort speedometer and approval symbol", () => {
		expect(source).toContain('import { ApprovalMenu, Choice, EffortMenu, ModelCapsuleLabel, useTriggerModifiers } from "../model-controls.ios";');
		expect(source).toContain('label={<ModelCapsuleLabel mark={snapshot.harness ? <HarnessImage harness={snapshot.harness} /> : null} title={parts.model || "Model"} />}');
		expect(source).toContain("<EffortMenu");
		expect(source).toContain("<ApprovalMenu");
		expect(source).toContain("symbol: approvalSymbol(choice.permissionMode, snapshot.harness)");
		expect(source).toContain('host: { alignSelf: "stretch", height: touchTarget }');
	});

	// The turn has no settings sheet: everything is in the row's own menus.
	it("lists the agent's models as checkable items, then any other setting", () => {
		expect(source).toContain("{modelRow?.choices.map((choice) => <Choice");
		expect(source).toContain("{otherRows.map((row) => <SettingPicker");
		expect(source).not.toContain("All turn settings");
		expect(source).not.toContain("Turn settings");
	});
});

describe("iOS other settings", () => {
	// A checkmark image on a button is not announced; a picker's choice is.
	it("offers each as a picker, or a toggle when it is on/off", () => {
		expect(source).toContain('modifiers={[pickerStyle("menu")]}');
		expect(source).toContain("<Text key={choice.value} modifiers={[tag(choice.value)]}>{choice.label}</Text>");
		expect(source).toContain("<Toggle label={row.label}");
		expect(source).not.toContain('systemImage={choice.selected ? "checkmark" : undefined}');
	});
});

describe("native approvals icon asset", () => {
	it("provides sharp 1x, 2x, and 3x variants for both appearances", () => {
		const root = new URL("../../assets/icons/CircleDashedCheck.imageset/", import.meta.url);
		expect(existsSync(new URL("Contents.json", root))).toBe(true);
		const catalog = JSON.parse(readFileSync(new URL("Contents.json", root), "utf8")) as {
			images: Array<{ filename: string; scale: string; appearances?: Array<{ appearance: string; value: string }> }>;
		};
		for (const appearance of ["light", "dark"]) {
			for (const scale of [1, 2, 3]) {
				const entry = catalog.images.find((image) => image.scale === `${scale}x` && (appearance === "light"
					? !image.appearances?.length
					: image.appearances?.some((item) => item.appearance === "luminosity" && item.value === "dark")));
				expect(entry).toBeDefined();
				const png = readFileSync(new URL(entry!.filename, root));
				expect(png.readUInt32BE(16)).toBe(18 * scale);
				expect(png.readUInt32BE(20)).toBe(18 * scale);
			}
		}
	});

	it("copies the asset into a clean generated iOS project", () => {
		const pluginUrl = new URL("../../plugins/withCircleDashedCheck.js", import.meta.url);
		expect(existsSync(pluginUrl)).toBe(true);
		const require = createRequire(import.meta.url);
		const { copyCircleDashedCheckAssets } = require(fileURLToPath(pluginUrl)) as { copyCircleDashedCheckAssets(root: string, projectName: string): void };
		const projectRoot = mkdtempSync(join(tmpdir(), "ao-icon-plugin-"));
		try {
			copyCircleDashedCheckAssets(projectRoot, "AO");
			const target = join(projectRoot, "AO", "Images.xcassets", "CircleDashedCheck.imageset");
			expect(readFileSync(join(target, "Contents.json"))).toEqual(readFileSync(new URL("../../assets/icons/CircleDashedCheck.imageset/Contents.json", import.meta.url)));
			expect(readFileSync(join(target, "circle-dashed-check-dark@3x.png"))).toEqual(readFileSync(new URL("../../assets/icons/CircleDashedCheck.imageset/circle-dashed-check-dark@3x.png", import.meta.url)));
		} finally {
			rmSync(projectRoot, { recursive: true, force: true });
		}
	});
});
