import { describe, expect, it } from "vitest";
import { APP_LOCALES } from "../i18n";
import { createFullAppI18n } from "../i18n/all-messages";
import {
	updateErrorSentence,
	type UpdateErrorInfo,
	type UpdateErrorKind,
	type UpdateErrorPhase,
} from "../../shared/update-error-message";
import { localizedUpdateError } from "./update-error-copy";

const PHASES: UpdateErrorPhase[] = ["check", "download", "install", "return"];
const KINDS: UpdateErrorKind[] = [
	"timeout",
	"unavailable",
	"rate-limited",
	"not-found",
	"http-error",
	"unreachable",
	"unreadable-feed",
	"invalid-signature",
	"detail",
	"generic",
];

function infos(): UpdateErrorInfo[] {
	return PHASES.flatMap((phase) => KINDS.map((kind) => ({
		kind,
		phase,
		...(kind === "http-error" ? { httpStatus: 403 } : {}),
		...(kind === "detail" ? { detail: "Something odd" } : {}),
	})));
}

describe("localizedUpdateError", () => {
	// The main process sends the English sentence as the fallback, so the English
	// catalog must say exactly the same thing.
	it("matches the main process's English sentence for every failure", () => {
		const { t } = createFullAppI18n("en");
		for (const info of infos()) {
			expect(localizedUpdateError(updateErrorSentence(info), info, "Update failed.", t)).toBe(updateErrorSentence(info));
		}
	});

	it.each(APP_LOCALES.filter((locale) => locale !== "en"))("has a complete %s sentence for every failure", (locale) => {
		const { t } = createFullAppI18n(locale);
		for (const info of infos()) {
			const text = localizedUpdateError(updateErrorSentence(info), info, "Update failed.", t);
			expect(text).not.toBe(updateErrorSentence(info));
			expect(text).not.toMatch(/\{\{|settings\.updates/);
			if (info.kind === "http-error") expect(text).toContain("403");
			if (info.kind === "detail") expect(text).toContain("Something odd");
		}
	});

	it("shows Japanese for a timed-out check", () => {
		const { t } = createFullAppI18n("ja");

		expect(localizedUpdateError("ignored", { kind: "timeout", phase: "check" }, "Update failed.", t)).toBe(
			"アップデートを確認できませんでした — アップデートサーバーの応答がタイムアウトしました。数分後にもう一度お試しください。",
		);
	});

	it("falls back to the text, guarded, when the main process sent no description", () => {
		const { t } = createFullAppI18n("ja");

		expect(localizedUpdateError("Not enough disk space to install the update.", undefined, "Update failed.", t)).toBe(
			"Not enough disk space to install the update.",
		);
		expect(localizedUpdateError("HttpError: 504\nHeaders: {}", undefined, "Update failed.", t)).toBe("Update failed.");
	});
});
