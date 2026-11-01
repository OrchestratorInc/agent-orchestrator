import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(new URL("./ReviewerComposer.tsx", import.meta.url), "utf8");

describe("reviewer attachment composer", () => {
	it("supports photos and bounded embedded text files", () => {
		expect(source).toContain("ImagePicker.launchImageLibraryAsync");
		expect(source).toContain("DocumentPicker.getDocumentAsync");
		expect(source).toContain("MAX_FILE_BYTES = 500_000");
		expect(source).toContain("<ChatAttachmentMenu");
	});

	it("only sends or interrupts and has no ordinary session actions", () => {
		expect(source).toContain("await onSend(");
		expect(source).toContain("onInterrupt()");
		for (const unsupported of ["onSteer", "onPromoteQueuedTurn", "onRollback", "onSettings", "onConfigOption"]) {
			expect(source).not.toContain(unsupported);
		}
	});

	// Android showed the reviewer a flat bar of its own; it is the session pill.
	it("draws the session composer's dock, glass pill and controls", () => {
		expect(source).toContain('from "./composerStyles"');
		expect(source).toContain("const makeStyles = composerStyleRules;");
		expect(source).toContain("<ComposerGlass radius={COMPOSER_RADIUS} />");
		expect(source).toContain("<MicKey");
		expect(source).toContain("style={[styles.input, listening && hiddenField]}");
		expect(source).not.toContain("onContentSizeChange");
	});

	// Dictation takes the field's place, as in the session composer; the banner
	// over the pill flashed "Keep holding…" on every tap.
	it("dictates into the pill, on a tap", () => {
		expect(source).toContain("{listening ? <DictationField state={voice.state} partial={voice.partial} subscribeLevel={voice.subscribeLevel} style={dictation} /> : null}");
		expect(source).toContain("onToggleLatched={voice.toggleLatched} />");
		expect(source).not.toContain("onPressIn");
		expect(source).not.toContain("Keep holding");
	});

	// The field is read-only while the reviewer is stopped, so dictating into it
	// must be too.
	it("disables dictation while it cannot take text", () => {
		expect(source).toContain("disabled={stopped || submitting}");
		expect(readFileSync(new URL("../voice/MicKey.tsx", import.meta.url), "utf8")).toContain("(disabledByOwner && !live)");
	});

	// The glyph on the accent fill is the accent's own ink, as in ChatComposer.
	it("inks the send glyph and spinner for the accent fill", () => {
		expect(source).toContain('<ActivityIndicator size="small" color={t.onAccent} />');
		expect(source).toContain('<AppSymbol name="send" size={iconSize.md} color={t.onAccent} />');
		expect(source).not.toContain("color={t.bgBase}");
	});

	it("rides the keyboard like the session composer", () => {
		expect(source).toContain("useReanimatedKeyboardAnimation()");
		expect(source).toContain("(restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value)");
		expect(source).toContain("paddingBottom: restingInset");
	});
});
