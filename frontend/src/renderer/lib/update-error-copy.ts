import type { TFunction } from "i18next";
import type { UpdateErrorInfo, UpdateErrorPhase } from "../../shared/update-error-message";
import { displayableUpdateError } from "../../shared/update-error-message";
import { appI18n } from "../i18n";

export function localizedUpdateError(
	text: string | undefined,
	info: UpdateErrorInfo | undefined,
	fallback: string,
	t: TFunction = appI18n.t,
): string {
	if (!info) return displayableUpdateError(text, fallback);
	const lead = updateErrorLead(info.phase, t);
	switch (info.kind) {
		case "generic":
			return t("settings.updates.error.generic", { lead });
		case "detail":
			return info.detail ? t("settings.updates.error.withReason", { lead, reason: info.detail }) : t("settings.updates.error.generic", { lead });
		default:
			return t("settings.updates.error.withReason", { lead, reason: updateErrorReason(info, t) });
	}
}

function updateErrorLead(phase: UpdateErrorPhase, t: TFunction): string {
	switch (phase) {
		case "check":
			return t("settings.updates.error.lead.check");
		case "download":
			return t("settings.updates.error.lead.download");
		case "install":
			return t("settings.updates.error.lead.install");
		case "return":
			return t("settings.updates.error.lead.return");
	}
}

function updateErrorReason(info: UpdateErrorInfo, t: TFunction): string {
	switch (info.kind) {
		case "timeout":
			return t("settings.updates.error.reason.timeout");
		case "unavailable":
			return t("settings.updates.error.reason.unavailable");
		case "rate-limited":
			return t("settings.updates.error.reason.rateLimited");
		case "not-found":
			return info.phase === "check" || info.phase === "return"
				? t("settings.updates.error.reason.notFoundInfo")
				: t("settings.updates.error.reason.notFoundFile");
		case "http-error":
			return t("settings.updates.error.reason.httpError", { status: info.httpStatus ?? "" });
		case "unreachable":
			return t("settings.updates.error.reason.unreachable");
		case "unreadable-feed":
			return t("settings.updates.error.reason.unreadableFeed");
		case "invalid-signature":
			return t("settings.updates.error.reason.invalidSignature");
		case "detail":
		case "generic":
			return "";
	}
}
