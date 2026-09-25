import { tokenizeShell } from '../../src/lib/highlightShell';

const types = (code: string) => tokenizeShell(code).filter((t) => t.type !== 'plain');

describe('tokenizeShell', () => {
  it('picks out comments, commands, flags and variables', () => {
    expect(types('# a note\ncurl -sS -X POST "$API/projects" -d x')).toEqual([
      { type: 'comment', text: '# a note' },
      { type: 'command', text: 'curl' },
      { type: 'flag', text: '-sS' },
      { type: 'flag', text: '-X' },
      { type: 'string', text: '"' },
      { type: 'variable', text: '$API' },
      { type: 'string', text: '/projects"' },
      { type: 'flag', text: '-d' },
    ]);
  });

  it('keeps a single-quoted JSON body spanning lines as one string', () => {
    const body = `'{\n  "name": "x", # not a comment\n  "a": "$NOT_A_VAR"\n}'`;
    expect(types(`-d ${body}`)).toEqual([
      { type: 'flag', text: '-d' },
      { type: 'string', text: body },
    ]);
  });

  it('reads keywords, and an assignment as plain', () => {
    const got = types('SA=$(curl x)\nwhile :; do\n  echo "$SA"\ndone');
    expect(got.map((t) => `${t.type}:${t.text}`)).toEqual([
      'command:curl',
      'keyword:while',
      'keyword:do',
      'command:echo',
      'string:"',
      'variable:$SA',
      'string:"',
      'keyword:done',
    ]);
    expect(tokenizeShell('SA=$(curl x)')[0]).toEqual({ type: 'plain', text: 'SA=$(' });
  });

  it('handles ${…} and escaped quotes inside double quotes', () => {
    expect(types('"${auth[@]}" "a\\"b"')).toEqual([
      { type: 'string', text: '"' },
      { type: 'variable', text: '${auth[@]}' },
      { type: 'string', text: '"' },
      // The space between the two is plain, so they are separate strings.
      { type: 'string', text: '"a\\"b"' },
    ]);
  });
});
