// Shared synthetic workload for the renderer regression and visible performance harness.
export const largeReviewFiles = Array.from({ length: 5123 }, (_, index) => ({
	path: `src/file-${String(index).padStart(5, "0")}.ts`,
	status: "modified",
	additions: 150,
	deletions: 150,
	size: 5000,
	binary: false,
	fileFingerprint: `fp-${index}`,
}));

export function largeReviewPatch(path: string) {
	// Substantial real patches, with trailing context to avoid full-file reads.
	return [
		`diff --git a/${path} b/${path}`, "index 1111111..2222222 100644",
		`--- a/${path}`, `+++ b/${path}`, "@@ -1,153 +1,153 @@",
		...Array.from({ length: 150 }, (_, index) => `-const old${index} = ${index};`),
		...Array.from({ length: 150 }, (_, index) => `+const next${index} = ${index + 1};`),
		" // unchanged context", " // more context", " // file continues", "",
	].join("\n");
}

