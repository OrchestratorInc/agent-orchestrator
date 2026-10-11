/**
 * Resolve a CSS custom property holding any color syntax (oklch, hex, …) to an
 * opaque `#rrggbb` string. Returns null when it cannot be resolved (jsdom, no
 * canvas, unset token).
 */
export function resolveCssColorToHex(property: string): string | null {
	try {
		const probe = document.createElement("span");
		probe.style.color = `var(${property})`;
		probe.style.display = "none";
		document.documentElement.appendChild(probe);
		const computed = getComputedStyle(probe).color;
		probe.remove();
		if (!computed) return null;

		const context = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
		if (!context) return null;
		context.clearRect(0, 0, 1, 1);
		context.fillStyle = computed;
		context.fillRect(0, 0, 1, 1);
		const [r, g, b, a] = context.getImageData(0, 0, 1, 1).data;
		if (a !== 255) return null;
		return `#${[r, g, b].map((channel) => channel.toString(16).padStart(2, "0")).join("")}`;
	} catch {
		return null;
	}
}
