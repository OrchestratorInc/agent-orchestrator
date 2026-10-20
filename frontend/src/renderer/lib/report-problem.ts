import { aoBridge } from "./bridge";
import { routeSurface } from "./telemetry";

export type ReportProblemDiagnostics = {
	appVersion: string;
	buildMode: string;
	daemonMessage?: string;
	daemonState: string;
	generatedAt: string;
	platform: string;
	routeSurface: string;
};

const REDACTED_LOCAL_PATH = "[redacted-local-path]";
const REDACTED_LOCAL_URL = "[redacted-local-url]";
const REDACTED_SECRET = "[redacted-secret]";
const GITHUB_NEW_ISSUE_URL = "https://github.com/OrchestratorInc/agent-orchestrator/issues/new";

const LOCAL_URL_PATTERN =
	/(?:\bfile:\/\/\/\S+|\bapp:\/\/renderer\/\S+|\bhttps?:\/\/(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?\S*)/gi;
const LOCAL_PATH_PATTERN = /(?:\/Users\/|\/home\/|\/tmp\/|\/private\/var\/|\/var\/folders\/)\S+|\b[A-Za-z]:[/\\][^\s)]+|\$HOME\b/gi;
const SECRET_KEY = String.raw`(?:client_?secret|refresh_?token|access_?token|id_?token|private_?key|pairing[_ -]?(?:code|password)|session[_ -]?secret|[A-Z0-9_-]*(?:TOKEN|SECRET|PASSWORD|API[_-]?KEY)[A-Z0-9_-]*|passwd|authorization|auth)`;
const QUOTED_VALUE = String.raw`(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`;
const QUERY_SECRET_PATTERN = new RegExp(String.raw`([?&]${SECRET_KEY}=)(?:${QUOTED_VALUE}|[^&\s#)]+)`, "gi");
const ESCAPED_JSON_SECRET_PATTERN = new RegExp(String.raw`^.*\\"${SECRET_KEY}\\"\s*:.*$`, "gim");
const JSON_SECRET_PATTERN = new RegExp(String.raw`((?:"${SECRET_KEY}"|'${SECRET_KEY}')\s*:\s*)(?:${QUOTED_VALUE}|[^\s,}\]]+)`, "gi");
const AUTHORIZATION_HEADER_PATTERN = /(\bAuthorization\s*:\s*)[^\r\n]+/gi;
const ASSIGNMENT_SECRET_PATTERN = new RegExp(String.raw`((?:\b|--)${SECRET_KEY}\s*(?:[:=]\s*|\s+))(?:${QUOTED_VALUE}|[^\s"';&)},\]]+)`, "gi");
const PAIRING_CODE_PATTERN = /(\b(?:pairing(?:[_ -]?code)?|code)\s*[:= -]?\s*)[0-9]{6,8}\b/gi;
const URL_USERINFO_PATTERN = /([a-z][a-z0-9+.-]*:\/\/)[^\s/?#]+@/gi;
const BEARER_SECRET_PATTERN = /\b(Bearer\s+)[A-Za-z0-9._~+/=-]+/gi;
const OPENAI_KEY_PATTERN = /\bsk-[A-Za-z0-9_-]+/g;
const GITHUB_TOKEN_PATTERN = /\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b/g;

export function sanitizeReportText(value: string): string {
	if (!value) return "";
	return value
		.replace(URL_USERINFO_PATTERN, "$1***@")
		.replace(LOCAL_URL_PATTERN, REDACTED_LOCAL_URL)
		.replace(LOCAL_PATH_PATTERN, REDACTED_LOCAL_PATH)
		.replace(QUERY_SECRET_PATTERN, `$1${REDACTED_SECRET}`)
		.replace(ESCAPED_JSON_SECRET_PATTERN, REDACTED_SECRET)
		.replace(JSON_SECRET_PATTERN, `$1"${REDACTED_SECRET}"`)
		.replace(AUTHORIZATION_HEADER_PATTERN, `$1${REDACTED_SECRET}`)
		.replace(ASSIGNMENT_SECRET_PATTERN, `$1${REDACTED_SECRET}`)
		.replace(PAIRING_CODE_PATTERN, `$1${REDACTED_SECRET}`)
		.replace(BEARER_SECRET_PATTERN, `$1${REDACTED_SECRET}`)
		.replace(OPENAI_KEY_PATTERN, REDACTED_SECRET)
		.replace(GITHUB_TOKEN_PATTERN, REDACTED_SECRET);
}

export async function collectReportProblemDiagnostics(now = new Date()): Promise<ReportProblemDiagnostics> {
	const [versionResult, daemonResult] = await Promise.allSettled([
		aoBridge.app.getVersion(),
		aoBridge.daemon.getStatus(),
	]);
	const daemonStatus = daemonResult.status === "fulfilled" ? daemonResult.value : undefined;

	return {
		appVersion: versionResult.status === "fulfilled" ? versionResult.value : "unknown",
		buildMode: import.meta.env.DEV ? "dev" : "packaged",
		daemonMessage: daemonStatus?.message,
		daemonState: daemonStatus?.state ?? "unknown",
		generatedAt: now.toISOString(),
		platform: typeof navigator === "undefined" ? "unknown" : navigator.platform || "unknown",
		routeSurface: typeof window === "undefined" ? "unknown" : routeSurface(currentRoutePath()),
	};
}

export function reportProblemDestinationUrl(diagnostics: ReportProblemDiagnostics): string {
	const url = new URL(GITHUB_NEW_ISSUE_URL);
	url.searchParams.set("title", "Report a problem");
	url.searchParams.set("body", `## What happened?\n\n\n## Safe diagnostics\n${formatDiagnostics(diagnostics)}`);
	return url.toString();
}

// Reports go to support in another timezone, so pair the reporter's local
// wall-clock time with an unambiguous UTC reference.
export function formatReportTimestamp(value: string, timeZone?: string): string {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	const local = new Intl.DateTimeFormat("en-US", {
		year: "numeric",
		month: "short",
		day: "numeric",
		hour: "numeric",
		minute: "2-digit",
		second: "2-digit",
		timeZoneName: "short",
		timeZone,
	}).format(date);
	const utc = date.toISOString().slice(0, 19).replace("T", " ");
	return `${local} (${utc} UTC)`;
}

function formatDiagnostics(diagnostics: ReportProblemDiagnostics): string {
	const lines = [
		`App version: ${sanitizeReportText(diagnostics.appVersion) || "unknown"}`,
		`Build mode: ${sanitizeReportText(diagnostics.buildMode) || "unknown"}`,
		`Platform: ${sanitizeReportText(diagnostics.platform) || "unknown"}`,
		`Route surface: ${sanitizeReportText(diagnostics.routeSurface) || "unknown"}`,
		`Daemon: ${sanitizeReportText(diagnostics.daemonState) || "unknown"}`,
		`Generated: ${sanitizeReportText(formatReportTimestamp(diagnostics.generatedAt)) || "unknown"}`,
	];
	if (diagnostics.daemonMessage?.trim()) {
		lines.push(`Daemon message: ${sanitizeReportText(diagnostics.daemonMessage.trim()).slice(0, 1_000)}`);
	}
	return lines.map((line) => `- ${line}`).join("\n");
}

function currentRoutePath(): string {
	const hashPath = window.location.hash.replace(/^#/, "").split("?")[0];
	if (hashPath?.startsWith("/")) return hashPath;
	return window.location.pathname;
}
