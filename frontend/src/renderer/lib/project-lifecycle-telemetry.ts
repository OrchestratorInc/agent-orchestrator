export function captureProjectAddFailed(
	capture: (event: string, properties: { error_code: string }) => void,
	code: string | undefined,
): void {
	capture("ao.projects.add_failed", { error_code: code || "unknown" });
}
