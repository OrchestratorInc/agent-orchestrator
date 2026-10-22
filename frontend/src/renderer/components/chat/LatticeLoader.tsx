/**
 * The live turn's working mark: a 3×3 lattice of dots with a short bright trail
 * running clockwise round the outer eight while the centre rests.
 *
 * Each outer dot plays the same brighten-and-fade beat, offset by an eighth of a
 * lap from the one before it, so three or so are lit at once and the light reads
 * as one trail travelling round the edge. The delays are negative, so the trail
 * is already moving on the first frame instead of starting from a dark grid.
 * It is pure CSS (see `.chat-lattice` in styles.css): the dots take the element's
 * own `color`, so they follow the theme, and under reduced motion the lattice
 * holds still.
 */

/** The outer dots in the order the trail visits them: clockwise from the top left. */
const TRAIL = [0, 1, 2, 5, 8, 7, 6, 3];
/** One lap round the edge. Matches the keyframe duration in styles.css. */
const LAP_MS = 880;

export function LatticeLoader() {
	return (
		<span aria-hidden="true" className="chat-lattice" data-testid="lattice-loader">
			{Array.from({ length: 9 }, (_, cell) => {
				const step = TRAIL.indexOf(cell);
				if (step < 0) return <span key={cell} className="chat-lattice-dot" />;
				return (
					<span
						key={cell}
						className="chat-lattice-dot"
						data-trail-step={step}
						style={{ animationDelay: `${Math.round((step * LAP_MS) / TRAIL.length) - LAP_MS}ms` }}
					/>
				);
			})}
		</span>
	);
}
