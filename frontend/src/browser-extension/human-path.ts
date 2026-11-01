// Curved pointer paths for `--human` clicks and drags: a cubic Bezier from the
// current point to the target with a little randomized bow and easing.

export type Point = { x: number; y: number };

export function humanPath(from: Point, to: Point, random: () => number = Math.random): Point[] {
	const dx = to.x - from.x;
	const dy = to.y - from.y;
	const distance = Math.hypot(dx, dy);
	const steps = Math.max(8, Math.min(40, Math.round(distance / 12)));
	// Perpendicular bow, up to 20% of the distance, either side.
	const bow = (random() - 0.5) * 0.4 * distance;
	const nx = distance === 0 ? 0 : -dy / distance;
	const ny = distance === 0 ? 0 : dx / distance;
	const c1 = { x: from.x + dx * 0.3 + nx * bow, y: from.y + dy * 0.3 + ny * bow };
	const c2 = { x: from.x + dx * 0.7 + nx * bow * 0.5, y: from.y + dy * 0.7 + ny * bow * 0.5 };
	const points: Point[] = [];
	for (let i = 1; i <= steps; i++) {
		const linear = i / steps;
		const t = linear < 0.5 ? 2 * linear * linear : 1 - (-2 * linear + 2) ** 2 / 2;
		const u = 1 - t;
		points.push({
			x: u ** 3 * from.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t ** 3 * to.x,
			y: u ** 3 * from.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t ** 3 * to.y,
		});
	}
	points[points.length - 1] = { ...to };
	return points;
}
