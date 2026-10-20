// Binary byte formatting for process memory readouts (status bar, dialogs).
// Mirrors the daemon CLI's formatter (`formatBytesCLI` in backend/internal/cli/ps.go):
// 1024-base units, one decimal below 100.
const UNITS = ["KB", "MB", "GB", "TB"] as const;

export function formatBytes(bytes: number): string {
	if (!Number.isFinite(bytes) || bytes < 1024) {
		return `${Math.max(0, Math.round(bytes))} B`;
	}
	let value = bytes;
	let divisions = 0;
	while (value >= 1024 && divisions < UNITS.length) {
		value /= 1024;
		divisions++;
	}
	const decimals = value >= 100 ? 0 : 1;
	const number = value.toFixed(decimals).replace(/\.0$/, "");
	return `${number} ${UNITS[divisions - 1]}`;
}
