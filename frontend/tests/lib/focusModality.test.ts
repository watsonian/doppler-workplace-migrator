import { describe, it, expect, afterEach } from 'vitest';
import { trackFocusModality } from '../../src/lib/focusModality';

/**
 * The two focus signals are mutually exclusive, and which one shows depends on
 * how you got to the field. That decision lives here: the stylesheet only reads
 * `data-modality` off the root.
 */
describe('focus modality', () => {
  let stop: (() => void) | null = null;

  afterEach(() => {
    stop?.();
    stop = null;
    delete document.documentElement.dataset.modality;
  });

  function start() {
    stop = trackFocusModality();
  }

  const modality = () => document.documentElement.dataset.modality;

  it('starts on keyboard, so focus is visible before anyone interacts', () => {
    // Guessing wrong this way costs a focus ring nobody needed. Guessing wrong
    // the other way leaves someone tabbing with no idea where they are.
    start();
    expect(modality()).toBe('keyboard');
  });

  it('switches to pointer on a click', () => {
    start();
    document.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    expect(modality()).toBe('pointer');
  });

  it('switches back on Tab', () => {
    start();
    document.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    expect(modality()).toBe('keyboard');
  });

  it('stays on pointer while you type into the field you clicked', () => {
    // The whole point: click into a field and it keeps the purple border while
    // you type. Treating every keypress as navigation would swap it for the
    // blue outline on the first character.
    start();
    document.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    for (const key of ['a', 'B', '7', '-']) {
      document.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
    }
    expect(modality()).toBe('pointer');
  });

  it('treats the other focus-moving keys as keyboard too', () => {
    for (const key of ['ArrowDown', 'Home', 'Escape', 'Enter', ' ']) {
      start();
      document.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
      document.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
      expect(modality(), `${key} should count as keyboard navigation`).toBe('keyboard');
      stop?.();
      stop = null;
    }
  });

  it('ignores a shortcut, which is someone reaching for a command', () => {
    start();
    document.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', metaKey: true, bubbles: true }));
    expect(modality()).toBe('pointer');
  });

  it('stops listening when torn down', () => {
    start();
    stop?.();
    stop = null;
    document.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    expect(modality()).toBe('keyboard');
  });
});
