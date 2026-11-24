/**
 * The board's name. The board is the Workers page; the route name argument is
 * kept so a restored route that still names the old Projects tab reads sanely.
 */
export function boardTitle(_route?: string): string {
	return "Workers";
}
