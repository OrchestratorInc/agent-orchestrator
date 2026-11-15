const SIDE = 4;
const TRAIL = [0, 1, 2, 3, 7, 11, 15, 14, 13, 12, 8, 4];
const LAP_MS = 1200;

export function LatticeLoader() {
	return (
		<span aria-hidden="true" className="chat-lattice" data-testid="lattice-loader">
			{Array.from({ length: SIDE * SIDE }, (_, cell) => {
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
