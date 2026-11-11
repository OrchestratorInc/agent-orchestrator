// ao-mascot.svg is generated from assets/brand/ruto.svg (see assets/brand/README.md).
import aoMascot from "../../../assets/ao-mascot.svg";

export function AOMascot({ className }: { className: string }) {
	return <img className={`inline-block object-contain ${className}`} src={aoMascot} alt="" aria-hidden="true" draggable={false} />;
}
