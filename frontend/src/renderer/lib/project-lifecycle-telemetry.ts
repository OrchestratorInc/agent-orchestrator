export function captureProjectAddFailed(
	capture: (event: string, properties: { error_code: string }) => void,
	code: string | undefined,
): void {
	capture("ao.renderer.project_add_failed", { error_code: code || "unknown" });
}
