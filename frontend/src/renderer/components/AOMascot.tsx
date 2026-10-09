import aoMascot from "../../../assets/ao-mascot.png";

export function AOMascot({ className }: { className: string }) {
	return (
		<span className={`relative inline-block ${className}`} aria-hidden="true">
			{/* Fill the artwork's transparent eye sockets on light backgrounds. */}
			<svg className="ao-mascot-eyes absolute inset-0 size-full" viewBox="0 0 1254 1254">
				<rect x="400" y="500" width="100" height="110" />
				<rect x="710" y="500" width="100" height="110" />
			</svg>
			<img className="relative size-full object-contain" src={aoMascot} alt="" />
		</span>
	);
}
