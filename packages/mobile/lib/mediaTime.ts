/** "1:05", or "1:02:05" past an hour, for a position or a length in seconds. */
export function formatMediaTime(seconds: number): string {
	const total = Number.isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const rest = String(total % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}` : `${minutes}:${rest}`;
}
