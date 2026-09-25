import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { Checkbox, Radio, Select, TextField } from '../../src/components/ui/primitives';

/**
 * The form controls, checked against the design system rather than against
 * themselves.
 *
 * These assert class names, which is usually a brittle thing to do. It earns its
 * place here because the properties being asserted are exactly the ones that
 * vanish silently: a control left on the browser default still works, still
 * looks like a control, and is only wrong next to the others.
 */
describe('form primitives', () => {
  describe('selection controls carry the product accent', () => {
    // Left to the browser these render in the OS accent, which on most machines
    // is blue — and blue means focus everywhere else in this product, so a blue
    // tick reads as "your keyboard is here" rather than "this is chosen".
    it('checkbox is purple, not the OS accent', () => {
      render(<Checkbox aria-label="pick" />);
      expect(screen.getByRole('checkbox')).toHaveClass('accent-purple-500');
    });

    it('radio is purple, not the OS accent', () => {
      render(<Radio aria-label="pick" />);
      expect(screen.getByRole('radio')).toHaveClass('accent-purple-500');
    });

    it('both are native controls, so the platform keeps the keyboard behaviour', () => {
      render(
        <>
          <Checkbox aria-label="a" />
          <Radio aria-label="b" />
        </>,
      );
      expect(screen.getByRole('checkbox').tagName).toBe('INPUT');
      expect(screen.getByRole('radio').tagName).toBe('INPUT');
    });

    it('sizes the two identically, since they sit in the same lists', () => {
      render(
        <>
          <Checkbox aria-label="a" />
          <Radio aria-label="b" />
        </>,
      );
      for (const el of [screen.getByRole('checkbox'), screen.getByRole('radio')]) {
        expect(el).toHaveClass('h-[15px]', 'w-[15px]');
      }
    });
  });

  describe('fields carry the product chrome', () => {
    /*
     * Taken from `.dp-input-group` in server/src/index.css, not from what these
     * happened to be built with. The values that were wrong before were wrong
     * in a way nothing catches: gray-100 instead of gray-75, a white background
     * instead of gray-25, 36px instead of 40 — each fine on its own, and all of
     * them together the reason the fields did not look like the product's.
     */
    const CHROME = [
      'bg-gray-25',          // --bg-normal, not white
      'hover:bg-gray-50',    // --bg-hover
      'border-gray-75',      // --border-normal
      'hover:border-gray-100', // --border-hover
      'text-gray-975',       // --text-normal
      'text-sm',             // 14px
      'rounded-md',          // 8px
      'h-10',                // md: 38px inner + 2px border
    ];

    it('TextField does', () => {
      render(<TextField id="a" label="A" />);
      const field = screen.getByLabelText('A');
      for (const cls of CHROME) expect(field).toHaveClass(cls);
      expect(field).toHaveClass('placeholder:text-gray-500');
    });

    it('Select does too, so the two read as siblings', () => {
      render(
        <Select id="b" label="B">
          <option value="x">X</option>
        </Select>,
      );
      const select = screen.getByLabelText('B');
      for (const cls of CHROME) expect(select).toHaveClass(cls);
    });

    it('leaves the focus border to the stylesheet, not a focus: utility', () => {
      // The purple border must fire on pointer focus and never on keyboard
      // focus, which depends on the last interaction rather than the element —
      // so it cannot be a `focus:` class here. Both controls carry the hook the
      // stylesheet keys off instead.
      const { unmount } = render(<TextField id="a" label="A" />);
      const field = screen.getByLabelText('A');
      expect(field).toHaveClass('dp-control');
      expect(field.className).not.toContain('focus:border-purple');
      unmount();

      render(
        <Select id="b" label="B">
          <option value="x">X</option>
        </Select>,
      );
      expect(screen.getByLabelText('B')).toHaveClass('dp-control');
    });

    it('invalid is red-600, the border the product uses', () => {
      render(<TextField id="a" label="A" invalid />);
      const field = screen.getByLabelText('A');
      expect(field).toHaveClass('border-red-600');
      // and it does not also carry the resting border to fight with
      expect(field).not.toHaveClass('border-gray-75');
    });

    it('is sans by default, and mono only where asked', () => {
      // The product's inputs are sans. Mono is reserved for identifiers, so the
      // host, token and CIDR fields opt in rather than every field defaulting.
      const { unmount } = render(<TextField id="a" label="A" />);
      expect(screen.getByLabelText('A')).not.toHaveClass('font-mono');
      unmount();

      render(<TextField id="b" label="B" mono />);
      expect(screen.getByLabelText('B')).toHaveClass('font-mono');
    });

    it('drops the OS control so the caret can be the system\'s', () => {
      render(
        <Select id="b" label="B">
          <option value="x">X</option>
        </Select>,
      );
      expect(screen.getByLabelText('B')).toHaveClass('appearance-none');
    });

    it('is reachable by its label', () => {
      render(
        <Select id="instance" label="Instance">
          <option value="cloud">Doppler.com</option>
        </Select>,
      );
      expect(screen.getByLabelText('Instance')).toBe(screen.getByRole('combobox'));
    });

    it('renders its options', () => {
      render(
        <Select id="instance" label="Instance" defaultValue="onprem">
          <option value="cloud">Doppler.com</option>
          <option value="onprem">Self-hosted</option>
        </Select>,
      );
      expect(screen.getByRole('option', { name: 'Self-hosted' })).toBeInTheDocument();
      expect(screen.getByRole('combobox')).toHaveValue('onprem');
    });
  });

  it('every control still takes a className without losing its own', () => {
    // The call sites add spacing. That must not be a way to drop the accent.
    render(<Radio aria-label="pick" className="mt-0.5" />);
    const radio = screen.getByRole('radio');
    expect(radio).toHaveClass('mt-0.5');
    expect(radio).toHaveClass('accent-purple-500');
  });
});
