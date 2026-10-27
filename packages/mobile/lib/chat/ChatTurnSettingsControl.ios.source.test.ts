import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(fileURLToPath(new URL("./ChatTurnSettingsControl.ios.tsx", import.meta.url)), "utf8");

describe("iOS turn settings menu trigger", () => {
	// The label used to be an invisible SwiftUI text in a fixed 220pt host with
	// the visible summary drawn over it, so a tap right of 220pt did nothing.
	it("draws the real summary as the menu's own label", () => {
		expect(source).toContain("<Menu key={generation} label={label} modifiers={modifiers}>");
		expect(source).not.toContain("opacity(0)");
		expect(source).not.toContain("width: 220");
		expect(source).toContain('host: { alignSelf: "stretch", height: touchTarget }');
		// A full touch target tall around the new-task sheet's filled capsule.
		expect(source).toContain("<HStack modifiers={[frame({ minHeight: touchTarget }), contentShape(shapes.rectangle())]}>");
		expect(source).toContain('frame({ minHeight: CAPSULE_HEIGHT, maxHeight: CAPSULE_HEIGHT }), backgroundOverlay({ color: t.bgElevated }), clipShape("capsule")');
		expect(source).toContain('accessibilityLabel(summary ? `Turn settings, ${summary}` : "Turn settings")');
	});

	it("sizes the label with Dynamic Type and marks it a pull-down", () => {
		// The new-task sheet's selector type.
		expect(source).toContain('font({ textStyle: "subheadline", weight: "medium" }), foregroundStyle(t.textSecondary), lineLimit(1), truncationMode("tail")');
		expect(source).toContain('<Image systemName="chevron.up.chevron.down"');
		expect(source).not.toMatch(/font\(\{ size:/);
	});
});

describe("iOS turn settings choices", () => {
	// A checkmark image on a button is not announced; a picker's choice is.
	it("offers each setting as a picker, or a toggle when it is on/off", () => {
		expect(source).toContain('modifiers={[pickerStyle("menu")]}');
		expect(source).toContain("<Text key={choice.value} modifiers={[tag(choice.value)]}>{choice.label}</Text>");
		expect(source).toContain("<Toggle label={row.label}");
		expect(source).not.toContain('systemImage={choice.selected ? "checkmark" : undefined}');
	});

	it("ends with the way into the full sheet", () => {
		expect(source).toContain('<Button label="All turn settings" systemImage={topicSymbols.setting} onPress={onOpenFallback} />');
	});

	it("shows a layers symbol on the model selector", () => {
		expect(source).toContain('model: "square.stack.3d.up"');
	});

	// The approval mode is its own control beside the model, so it is never
	// read as part of the model: the mode's symbol alone, named to VoiceOver,
	// with each choice in its menu marked by its own.
	it("puts the approval mode on its own icon-only control", () => {
		expect(source).toContain('const approvalRow = rows.find((row) => turnSettingTopic(row) === "permissions");');
		expect(source).toContain("systemName={approvalSymbol(approvalSelection?.permissionMode, snapshot.harness)}");
		expect(source).toContain("frame({ width: touchTarget, height: touchTarget })");
		expect(source).toContain("accessibilityLabel(`${approvalRow.label}, ${parts.approval || approvalRow.value}`)");
		expect(source).toContain("systemImage={approvalSymbol(choice.permissionMode, snapshot.harness)}");
		expect(source).toContain("{modelRows.map((row) => <SettingPicker");
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
