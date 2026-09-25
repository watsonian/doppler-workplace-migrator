/**
 * Tracks whether the last interaction was a keypress or a pointer, and records
 * it as `data-modality` on the document element.
 *
 * Why this exists rather than plain `:focus-visible`: the product shows two
 * different focus signals and keeps them apart — a purple border when you click
 * into a field, a blue outline when you tab to it, never both. It does that with
 * React Aria's modality tracking, where the two states are defined as
 * `[data-focus]:not([data-focus-visible])` and `[data-focus-visible]`
 * (frontend/tailwind.config.ts).
 *
 * Native `:focus-visible` cannot express it. Browsers deliberately match
 * `:focus-visible` on a *clicked* text input — the heuristic says anything that
 * takes typed input should always show focus — so a click would light the blue
 * outline as well as the purple border. Keying off the last interaction instead
 * reproduces what the product actually does.
 *
 * Defaults to keyboard: the failure mode of guessing wrong that way is a focus
 * ring nobody needed, and the other way is no visible focus at all.
 */

export type FocusModality = 'keyboard' | 'pointer';

/** Keys that move focus. Typing into a field should not turn the outline on. */
const NAVIGATION_KEYS = new Set([
  'Tab',
  'ArrowUp',
  'ArrowDown',
  'ArrowLeft',
  'ArrowRight',
  'Home',
  'End',
  'PageUp',
  'PageDown',
  'Escape',
  'Enter',
  ' ',
]);

function set(modality: FocusModality) {
  document.documentElement.dataset.modality = modality;
}

/**
 * Installs the listeners. Safe to call more than once; returns a teardown so
 * tests and hot reloads do not stack handlers.
 */
export function trackFocusModality(): () => void {
  if (typeof document === 'undefined') return () => {};

  set('keyboard');

  function onKeyDown(e: KeyboardEvent) {
    // A modifier on its own is someone reaching for a shortcut, not navigating.
    if (e.metaKey || e.altKey || e.ctrlKey) return;
    if (NAVIGATION_KEYS.has(e.key)) set('keyboard');
  }

  function onPointerDown() {
    set('pointer');
  }

  // Capture, so a handler that stops propagation cannot strand the modality on
  // a stale value and leave focus unindicated.
  document.addEventListener('keydown', onKeyDown, true);
  document.addEventListener('pointerdown', onPointerDown, true);
  document.addEventListener('mousedown', onPointerDown, true);
  document.addEventListener('touchstart', onPointerDown, true);

  return () => {
    document.removeEventListener('keydown', onKeyDown, true);
    document.removeEventListener('pointerdown', onPointerDown, true);
    document.removeEventListener('mousedown', onPointerDown, true);
    document.removeEventListener('touchstart', onPointerDown, true);
  };
}
