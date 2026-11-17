import type { HardwareReturnProps } from "./HardwareReturn.types";

/**
 * Return-to-send from a hardware keyboard is iOS-only for now (iPad keyboard
 * cases). Elsewhere the field renders exactly as before, with no wrapper in the
 * row, and Return keeps inserting a newline.
 */
export function HardwareReturn({ children }: HardwareReturnProps) {
	return <>{children}</>;
}
