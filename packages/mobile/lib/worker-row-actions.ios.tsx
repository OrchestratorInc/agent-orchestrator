import { Host } from "@expo/ui";
import { Button, HStack, Image } from "@expo/ui/swift-ui";
import {
	accessibilityIdentifier,
	accessibilityLabel,
	buttonBorderShape,
	buttonStyle,
	controlSize,
	frame,
	labelStyle,
	tint,
} from "@expo/ui/swift-ui/modifiers";
import { useTheme, useThemeState } from "./ThemeProvider";
import { iconSize, touchTarget } from "./tokens";
import { workerActionSymbol } from "./worker-action-model";
import { WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";

const ACTION_WIDTH = WORKER_ACTION_REVEAL_WIDTH / 2;
const CONTROL_SIZE = touchTarget;
// Taller than the buttons so their glass is not clipped at the host's edge. The
// swipe rail centres this block on the row, however tall the row grows.
const HOST_HEIGHT = 76;
// The glass button hugs its label, so each glyph gets the same box. Without
// it the taller `pin` symbol drew a larger circle than the archive box.
const GLYPH_BOX = 24;

export function WorkerRowActions({
	title,
	pinned,
	onSetPinned,
	onDelete,
}: {
	title: string;
	pinned: boolean;
	onSetPinned(pinned: boolean): void;
	onDelete(): void;
}) {
	const { scheme } = useThemeState();
	const t = useTheme();

	return (
		<Host style={{ width: ACTION_WIDTH * 2, height: HOST_HEIGHT }} colorScheme={scheme}>
			{/* Deliberately not a `GlassEffectContainer`. That is the right wrapper
			    when two glass controls should read as one surface, but inside one the
			    system blends their material as they approach, so pin and archive
			    morphed into a single blob mid-swipe. Each button keeps its own glass
			    here, and the 16pt gap is what separates them. */}
			<HStack spacing={16} modifiers={[frame({ width: ACTION_WIDTH * 2, height: HOST_HEIGHT })]}>
				<Button
					onPress={() => onSetPinned(!pinned)}
					modifiers={[
						buttonStyle("glass"),
						buttonBorderShape("circle"),
						controlSize("large"),
						labelStyle("iconOnly"),
						// Foreground ink, not a hue: `amber` is the palette's "needs your
						// attention" colour, so a yellow pin claimed this row wanted you when
						// it was only pinned. The state is carried by the glyph — `pin.fill`
						// against `pin` — which is how the palette says emphasis works.
						tint(t.textPrimary),
						frame({ width: CONTROL_SIZE, height: CONTROL_SIZE }),
						accessibilityLabel(pinned ? `Unpin ${title}` : `Pin ${title}`),
						accessibilityIdentifier("worker-pin"),
					]}
				>
					<Image
						systemName={pinned ? "pin.fill" : "pin"}
						size={iconSize.lg}
						color={t.textPrimary}
						modifiers={[frame({ width: GLYPH_BOX, height: GLYPH_BOX })]}
					/>
				</Button>
				{/* The destructive action of the pair: it stops the agent and deletes
				    a clean worktree, so it takes the system's destructive role and red. */}
				<Button
					role="destructive"
					onPress={onDelete}
					modifiers={[
						buttonStyle("glass"),
						buttonBorderShape("circle"),
						controlSize("large"),
						labelStyle("iconOnly"),
						tint(t.red),
						frame({ width: CONTROL_SIZE, height: CONTROL_SIZE }),
						accessibilityLabel(`Archive ${title}`),
						accessibilityIdentifier("worker-archive"),
					]}
				>
					<Image
						systemName={workerActionSymbol("delete").ios}
						size={iconSize.lg}
						color={t.red}
						modifiers={[frame({ width: GLYPH_BOX, height: GLYPH_BOX })]}
					/>
				</Button>
			</HStack>
		</Host>
	);
}
