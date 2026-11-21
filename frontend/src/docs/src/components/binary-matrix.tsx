"use client";

import { useEffect, useRef } from "react";

const CELL = 16;
const FONT_SIZE = 9;
// Sky-to-blue tones (Tailwind sky-300, sky-400, blue-400, blue-500); each cell
// takes one at random, so the field reads as a mix of the two.
const TONES = ["#7dd3fc", "#38bdf8", "#60a5fa", "#3b82f6"];
// Resting cells sit between these alphas and slowly reshuffle. Kept faint, so the
// matrix reads as texture and only the cells under the pointer stand out.
const BASE_MIN = 0.03;
const BASE_MAX = 0.08;
// Extra alpha a fully lit cell gains under the pointer.
const SHINE = 0.45;
const RADIUS = 130;
// Seconds for a lit cell's energy to fall to ~37%, leaving a trail.
const DECAY = 0.7;
// Times per second a lit cell re-rolls its digit, and the odds it lands on 1.
const FLIP_RATE = 7;
const ONE_CHANCE = 0.45;
// Share of resting cells that pick a new alpha each second.
const TWINKLE_RATE = 0.06;
const IDLE_FRAME_MS = 120;

function randomBase() {
  return BASE_MIN + Math.random() * (BASE_MAX - BASE_MIN);
}

function randomTone() {
  return Math.floor(Math.random() * TONES.length);
}

/**
 * Field of faint 0s behind every docs page: the texture the marketing site
 * (orchestrator.inc) runs behind its home page, so keep the two in step. Cells
 * near a moving pointer shine and flicker to 1s, then fade back. Only cells
 * that change are repainted, so a frame costs a few hundred blits rather than
 * the whole screen. Paused offscreen and in hidden tabs; reduced-motion users
 * get no ambient twinkle.
 */
export function BinaryMatrix({ className = "" }: { className?: string }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const context = canvas?.getContext("2d");
    if (!canvas || !context) return;

    const reducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)",
    ).matches;

    // One glyph per tone and digit, drawn once and blitted with globalAlpha.
    const sprites = TONES.map(() => [
      document.createElement("canvas"),
      document.createElement("canvas"),
    ]);

    let ratio = 1;
    let width = 0;
    let height = 0;
    let columns = 0;
    let rows = 0;
    let base = new Float32Array(0);
    let energy = new Float32Array(0);
    let glyph = new Uint8Array(0);
    let tone = new Uint8Array(0);
    // Cells to repaint this frame, flagged so each is queued once.
    let dirtyFlags = new Uint8Array(0);
    let dirty: number[] = [];
    // Latest pointer position, applied once per frame and only after it moves.
    let pointer: { x: number; y: number } | null = null;
    let moved = false;
    let lit = false;
    let frame = 0;
    let lastTime = 0;
    let lastDraw = 0;
    let visible = true;

    const paintSprites = () => {
      const family = getComputedStyle(canvas).fontFamily;
      sprites.forEach((digits, toneIndex) => {
        digits.forEach((sprite, digit) => {
          sprite.width = sprite.height = Math.ceil(CELL * ratio);
          const spriteContext = sprite.getContext("2d");
          if (!spriteContext) return;
          spriteContext.setTransform(ratio, 0, 0, ratio, 0, 0);
          spriteContext.font = `${FONT_SIZE}px ${family}`;
          spriteContext.textAlign = "center";
          spriteContext.textBaseline = "middle";
          spriteContext.fillStyle = TONES[toneIndex];
          spriteContext.fillText(String(digit), CELL / 2, CELL / 2);
        });
      });
    };

    const resize = () => {
      ratio = Math.min(window.devicePixelRatio || 1, 2);
      width = canvas.clientWidth;
      height = canvas.clientHeight;
      columns = Math.ceil(width / CELL);
      rows = Math.ceil(height / CELL);
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
      context.setTransform(ratio, 0, 0, ratio, 0, 0);

      const size = columns * rows;
      base = Float32Array.from({ length: size }, randomBase);
      energy = new Float32Array(size);
      glyph = new Uint8Array(size);
      tone = Uint8Array.from({ length: size }, randomTone);
      dirtyFlags = new Uint8Array(size);
      dirty = [];
      paintSprites();
      drawAll();
    };

    const markDirty = (index: number) => {
      if (dirtyFlags[index]) return;
      dirtyFlags[index] = 1;
      dirty.push(index);
    };

    const paintCell = (index: number, offsetX: number, offsetY: number) => {
      context.globalAlpha = Math.min(base[index] + energy[index] * SHINE, 0.55);
      context.drawImage(
        sprites[tone[index]][glyph[index]],
        offsetX + (index % columns) * CELL,
        offsetY + Math.floor(index / columns) * CELL,
        CELL,
        CELL,
      );
    };

    const drawAll = () => {
      context.clearRect(0, 0, width, height);
      const offsetX = Math.floor((width - columns * CELL) / 2);
      const offsetY = Math.floor((height - rows * CELL) / 2);
      for (let index = 0; index < base.length; index++) paintCell(index, offsetX, offsetY);
      context.globalAlpha = 1;
      for (const index of dirty) dirtyFlags[index] = 0;
      dirty = [];
    };

    const drawDirty = () => {
      const offsetX = Math.floor((width - columns * CELL) / 2);
      const offsetY = Math.floor((height - rows * CELL) / 2);
      for (const index of dirty) {
        context.clearRect(
          offsetX + (index % columns) * CELL,
          offsetY + Math.floor(index / columns) * CELL,
          CELL,
          CELL,
        );
        paintCell(index, offsetX, offsetY);
        dirtyFlags[index] = 0;
      }
      context.globalAlpha = 1;
      dirty = [];
    };

    const update = (elapsed: number) => {
      const fade = Math.exp(-elapsed / DECAY);
      const flipChance = FLIP_RATE * elapsed;
      lit = false;

      for (let index = 0; index < energy.length; index++) {
        if (energy[index] > 0.01) {
          energy[index] *= fade;
          lit = true;
          markDirty(index);
        } else if (energy[index] > 0) {
          energy[index] = 0;
          glyph[index] = 0;
          markDirty(index);
        }
      }

      if (pointer && moved) {
        moved = false;
        const offsetX = Math.floor((width - columns * CELL) / 2);
        const offsetY = Math.floor((height - rows * CELL) / 2);
        const reach = Math.ceil(RADIUS / CELL);
        const centerColumn = Math.floor((pointer.x - offsetX) / CELL);
        const centerRow = Math.floor((pointer.y - offsetY) / CELL);
        for (let row = centerRow - reach; row <= centerRow + reach; row++) {
          if (row < 0 || row >= rows) continue;
          for (
            let column = centerColumn - reach;
            column <= centerColumn + reach;
            column++
          ) {
            if (column < 0 || column >= columns) continue;
            const dx = offsetX + (column + 0.5) * CELL - pointer.x;
            const dy = offsetY + (row + 0.5) * CELL - pointer.y;
            const strength = 1 - Math.hypot(dx, dy) / RADIUS;
            if (strength <= 0) continue;
            const index = row * columns + column;
            energy[index] = Math.max(energy[index], strength * strength);
            lit = true;
            markDirty(index);
          }
        }
      }

      for (let index = 0; index < energy.length; index++) {
        if (energy[index] > 0.12 && Math.random() < flipChance) {
          glyph[index] = Math.random() < ONE_CHANCE ? 1 : 0;
          tone[index] = randomTone();
          markDirty(index);
        }
      }

      if (!reducedMotion) {
        const twinkles = Math.round(base.length * TWINKLE_RATE * elapsed);
        for (let step = 0; step < twinkles; step++) {
          const index = Math.floor(Math.random() * base.length);
          base[index] = randomBase();
          tone[index] = randomTone();
          markDirty(index);
        }
      }
    };

    const tick = (time: number) => {
      frame = requestAnimationFrame(tick);
      // Nothing lit means only the slow twinkle is moving, so draw less.
      if (!lit && !moved && time - lastDraw < IDLE_FRAME_MS) return;
      if (reducedMotion && !lit && !moved) return;

      const elapsed = lastTime ? Math.min((time - lastTime) / 1000, 0.1) : 0;
      lastTime = time;
      lastDraw = time;
      update(elapsed);
      drawDirty();
    };

    const start = () => {
      if (frame || !visible || document.hidden) return;
      lastTime = 0;
      frame = requestAnimationFrame(tick);
    };

    const stop = () => {
      cancelAnimationFrame(frame);
      frame = 0;
    };

    // The canvas is fixed to the viewport, so its rect only changes on resize.
    let rect = canvas.getBoundingClientRect();
    const handlePointerMove = (event: PointerEvent) => {
      const x = event.clientX - rect.left;
      const y = event.clientY - rect.top;
      pointer = x >= 0 && y >= 0 && x <= rect.width && y <= rect.height
        ? { x, y }
        : null;
      moved = pointer !== null;
    };
    const clearPointer = () => {
      pointer = null;
    };

    const resizeObserver = new ResizeObserver(() => {
      rect = canvas.getBoundingClientRect();
      resize();
    });
    resizeObserver.observe(canvas);

    const intersectionObserver = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting;
      if (visible) start();
      else stop();
    });
    intersectionObserver.observe(canvas);

    const handleVisibility = () => {
      if (document.hidden) stop();
      else start();
    };

    window.addEventListener("pointermove", handlePointerMove, {
      passive: true,
    });
    document.documentElement.addEventListener("pointerleave", clearPointer);
    window.addEventListener("blur", clearPointer);
    document.addEventListener("visibilitychange", handleVisibility);

    resize();
    start();
    // The mono webfont may land after the first paint; redraw the glyphs
    // with it once it does.
    document.fonts?.ready.then(() => {
      paintSprites();
      drawAll();
    });

    return () => {
      stop();
      resizeObserver.disconnect();
      intersectionObserver.disconnect();
      window.removeEventListener("pointermove", handlePointerMove);
      document.documentElement.removeEventListener("pointerleave", clearPointer);
      window.removeEventListener("blur", clearPointer);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      aria-hidden="true"
      className={`pointer-events-none size-full font-mono ${className}`}
    />
  );
}
