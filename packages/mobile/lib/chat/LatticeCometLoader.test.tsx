import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

type Loop = { value: unknown; config: { toValue: number; duration: number; useNativeDriver: boolean }; started: number; stopped: number };

const fixtures = vi.hoisted(() => ({
	reduceMotion: false,
	values: [] as { set: number[] }[],
	loops: [] as Loop[],
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	class Value {
		set: number[] = [];
		constructor() {
			fixtures.values.push(this);
		}
		setValue(value: number) {
			this.set.push(value);
		}
		interpolate(config: unknown) {
			return { interpolates: this, config };
		}
	}
	return {
		Animated: {
			View: host("AnimatedView"),
			Value,
			timing: (value: unknown, config: Loop["config"]) => ({ value, config }),
			loop: (timing: { value: unknown; config: Loop["config"] }) => {
				const record: Loop = { ...timing, started: 0, stopped: 0 };
				fixtures.loops.push(record);
				return { start: () => void record.started++, stop: () => void record.stopped++ };
			},
		},
		Easing: { linear: (t: number) => t },
		PixelRatio: { roundToNearestPixel: (value: number) => value },
		StyleSheet: { create: (styles: unknown) => styles },
		View: host("View"),
	};
});
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => fixtures.reduceMotion }));

import { LatticeCometLoader } from "./LatticeCometLoader";
import { LatticeActiveContext } from "./latticeActivity";
import { LATTICE_LAP_MS, LATTICE_TRAIL } from "./latticeTrail";

let renderer: ReactTestRenderer | undefined;
const render = (active: boolean, count = 1) => (
	<LatticeActiveContext.Provider value={active}>
		{Array.from({ length: count }, (_, i) => <LatticeCometLoader key={i} color="#f80" />)}
	</LatticeActiveContext.Provider>
);

beforeEach(() => {
	fixtures.reduceMotion = false;
	fixtures.values.length = 0;
	fixtures.loops.length = 0;
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

describe("the board's lattice", () => {
	it("laps natively, a step per edge dot, with no JavaScript clock", () => {
		act(() => {
			renderer = create(render(true));
		});
		expect(fixtures.loops).toHaveLength(1);
		const [lap] = fixtures.loops;
		expect(lap!.config).toMatchObject({ toValue: LATTICE_TRAIL.length, duration: LATTICE_LAP_MS, useNativeDriver: true });
		expect(lap!.started).toBe(1);
		expect(renderer!.root.findAllByType("AnimatedView" as never)).toHaveLength(3);
	});

	it("gives each lattice its own value, so one mounting or leaving never moves another", () => {
		act(() => {
			renderer = create(render(true, 2));
		});
		expect(fixtures.values).toHaveLength(2);
		expect(fixtures.loops.map((lap) => lap.value)).toEqual(fixtures.values);
	});

	it("rests while it cannot be seen, and laps again from the start when it can", () => {
		act(() => {
			renderer = create(render(false));
		});
		expect(fixtures.loops).toHaveLength(0);
		act(() => renderer!.update(render(true)));
		expect(fixtures.loops).toHaveLength(1);
		act(() => renderer!.update(render(false)));
		expect(fixtures.loops[0]!.stopped).toBe(1);
		expect(fixtures.values[0]!.set).toEqual([0]);
		act(() => renderer!.update(render(true)));
		expect(fixtures.loops).toHaveLength(2);
		expect(fixtures.loops[1]!.started).toBe(1);
	});

	it("stops its lap when it unmounts", () => {
		act(() => {
			renderer = create(render(true));
		});
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(fixtures.loops[0]!.stopped).toBe(1);
	});

	it("holds still, with no comet, under Reduce Motion", () => {
		fixtures.reduceMotion = true;
		act(() => {
			renderer = create(render(true));
		});
		expect(fixtures.loops).toHaveLength(0);
		expect(renderer!.root.findAllByType("AnimatedView" as never)).toHaveLength(0);
	});
});
