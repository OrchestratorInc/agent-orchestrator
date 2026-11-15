// What this phone holds of one Cloud account's data outside React state (a
// closed chat's events, pull request details), forgotten when the account
// changes: on sign-out and an account switch, so the next account never reads
// the last one's. Each cache registers its own clear; the Cloud provider runs
// them all when it resets.

const clears = new Set<() => void>();

export function forgetOnCloudAccountChange(clear: () => void): void {
	clears.add(clear);
}

export function forgetCloudAccountCaches(): void {
	for (const clear of clears) clear();
}
