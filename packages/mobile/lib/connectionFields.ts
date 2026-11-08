/** Validation shared by the forms that take a machine's address by hand. */

export function isValidPort(port: string): boolean {
	return /^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535;
}

export const PORT_ERROR = "Enter a port from 1 to 65535.";

/**
 * The port's error, said only once something is typed: an empty port just
 * keeps the form from submitting.
 */
export function portError(port: string): string | undefined {
	return port.trim() && !isValidPort(port) ? PORT_ERROR : undefined;
}
