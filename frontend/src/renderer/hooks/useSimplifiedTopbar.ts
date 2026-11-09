import { isSimplifiedTopbar } from "../lib/simplified-topbar";
import { useUiStore } from "../stores/ui-store";

/**
 * The simplified, floating top bar is a Developer Mode preview (with its own
 * switch there). Everyone else keeps the previous top bar until it graduates.
 */
export function useSimplifiedTopbar(): boolean {
	return useUiStore(isSimplifiedTopbar);
}
