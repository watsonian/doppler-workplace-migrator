import type { ReactNode } from 'react';

/**
 * A small highlighter for the shell the permissions page shows: comments,
 * single- and double-quoted strings (with variables inside double quotes),
 * variables, a handful of keywords and commands, and flags.
 *
 * Written here rather than pulling in a highlighting library for one block of
 * known, simple shell. It scans the whole text rather than line by line,
 * because a single-quoted JSON body spans several lines.
 */

export type ShellToken = 'plain' | 'comment' | 'string' | 'variable' | 'keyword' | 'command' | 'flag';

const KEYWORDS = new Set(['while', 'do', 'done', 'for', 'in', 'if', 'then', 'else', 'fi', 'break', 'exit', 'true']);
const COMMANDS = new Set(['curl', 'jq', 'echo', 'doppler']);

/** Class for each token, for a dark background. */
const CLASSES: Record<ShellToken, string> = {
  plain: '',
  comment: 'text-[#8B93A1] italic',
  string: 'text-[#9FE3B6]',
  variable: 'text-[#C8A7FB]',
  keyword: 'text-[#FF9EC6]',
  command: 'text-[#7CB4FF]',
  flag: 'text-[#F5C66B]',
};

export function tokenizeShell(code: string): { type: ShellToken; text: string }[] {
  const out: { type: ShellToken; text: string }[] = [];
  const push = (type: ShellToken, text: string) => {
    if (!text) return;
    const last = out[out.length - 1];
    if (last && last.type === type) last.text += text;
    else out.push({ type, text });
  };

  // A variable at i: $NAME, ${…}, $@, $?, $1. Returns its length, or 0.
  const variableAt = (i: number): number => {
    if (code[i] !== '$') return 0;
    const next = code[i + 1];
    if (next === '{') {
      const end = code.indexOf('}', i + 2);
      return end === -1 ? 0 : end - i + 1;
    }
    const m = /^[A-Za-z_][A-Za-z0-9_]*|^[@?#0-9]/.exec(code.slice(i + 1));
    return m ? m[0].length + 1 : 0;
  };

  let i = 0;
  while (i < code.length) {
    const c = code[i];
    const atWordStart = i === 0 || /[\s;|&(]/.test(code[i - 1]);

    if (c === '#' && atWordStart) {
      const end = code.indexOf('\n', i);
      const stop = end === -1 ? code.length : end;
      push('comment', code.slice(i, stop));
      i = stop;
      continue;
    }

    if (c === "'") {
      const end = code.indexOf("'", i + 1);
      const stop = end === -1 ? code.length : end + 1;
      push('string', code.slice(i, stop));
      i = stop;
      continue;
    }

    if (c === '"') {
      // Up to the next unescaped quote, with variables picked out inside.
      push('string', '"');
      i++;
      while (i < code.length && code[i] !== '"') {
        if (code[i] === '\\' && i + 1 < code.length) {
          push('string', code.slice(i, i + 2));
          i += 2;
          continue;
        }
        const v = variableAt(i);
        if (v) {
          push('variable', code.slice(i, i + v));
          i += v;
          continue;
        }
        push('string', code[i]);
        i++;
      }
      if (i < code.length) {
        push('string', '"');
        i++;
      }
      continue;
    }

    const v = variableAt(i);
    if (v) {
      push('variable', code.slice(i, i + v));
      i += v;
      continue;
    }

    if (c === '-' && atWordStart) {
      const m = /^-{1,2}[A-Za-z][A-Za-z-]*/.exec(code.slice(i));
      if (m) {
        push('flag', m[0]);
        i += m[0].length;
        continue;
      }
    }

    if (/[A-Za-z_]/.test(c) && atWordStart) {
      const m = /^[A-Za-z_][A-Za-z0-9_-]*/.exec(code.slice(i))!;
      const word = m[0];
      const after = code[i + word.length];
      // A word followed by "=" is an assignment, not a command.
      const type: ShellToken =
        after === '=' ? 'plain' : KEYWORDS.has(word) ? 'keyword' : COMMANDS.has(word) ? 'command' : 'plain';
      push(type, word);
      i += word.length;
      continue;
    }

    push('plain', c);
    i++;
  }
  return out;
}

export function highlightShell(code: string): ReactNode[] {
  return tokenizeShell(code).map((t, i) =>
    t.type === 'plain' ? t.text : (
      <span key={i} className={CLASSES[t.type]}>
        {t.text}
      </span>
    ),
  );
}
