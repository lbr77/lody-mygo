// Generates lody.json.gz, the golden copies convmd's tests compare with:
// Lody's own buildConversationMarkdown run on histories that use only what
// our session projection keeps. Run it with bun beside a src/ holding
// copies of Lody's packages/shared/src conversation-markdown.ts,
// replay-prompt-builder.ts and comment-reference-format.ts, empty ai.ts and
// schema.ts, and a session-link-export.ts whose
// normalizeSessionLinksForExport returns its input (the real one needs
// mdast; these histories have no session links):
//
//	TZ=UTC bun lody.ts lody.json.gz
import { buildConversationMarkdown } from './src/conversation-markdown';

let id = 0;
const base = Date.parse('2026-09-08T14:00:00.000Z');
const at = (s: number) => new Date(base + s * 1000).toISOString();
const entry = (role: string, items: any[], extra: any = {}) => {
  id += 1;
  return { id: `e${id}`, role, timestamp: at(id * 7), items, ...extra };
};
const text = (t: string) => ({ type: 'text', text: t });
const tool = (title: string, kind: string, content: any[], extra: any = {}) => ({
  type: 'tool_call', toolCallId: `t${(id += 1)}`, title, kind, status: 'completed', content, ...extra,
});
const out = (t: string) => ({ type: 'content', content: { type: 'text', text: t } });
const term = (cmd: string, args: string[], output: string) => [
  { type: 'terminal_command', command: cmd, args },
  { type: 'terminal_output', output },
];

const small = [
  entry('system', [text('system notice')]),
  entry('user', [text('# Fix the build\nIt fails on `make`.'), { type: 'file', fileName: 'build\nlog.txt', sizeBytes: 5001, mimeType: 'text/plain' }], { userId: 'u-ada' }),
  entry('assistant', [
    { type: 'thought', text: '## Plan\nLook at the `Makefile` first.\n```sh\n# keep me\n```' },
    { type: 'plan', entries: [{ content: 'Read Makefile', status: 'completed' }, { content: 'Fix target', status: 'in_progress' }, { content: 'Run tests', status: 'pending' }] },
    tool('Run make', 'execute', term('make', ['-j', '4'], 'error: token ghp_' + 'a'.repeat(30) + ' bad\ncloning https://bob:secret@github.com/x.git\nAuthorization: bearer ' + 'z'.repeat(24) + '\n')),
    tool('Read Makefile', 'read', [out('all:\n\t```\n\tcc main.c <stdin> & more\n```')]),
    tool('Edit <Makefile> & friends', 'edit', [{ type: 'diff', path: 'Makefile', oldText: 'a', newText: 'b' }, { type: 'diff', path: 'src/x.c', oldText: null, newText: 'c' }]),
    tool('', 'other', []),
    { type: 'subagent_task', taskId: 's1', description: '  Explore\nthe repo  ', status: 'completed' },
    { type: 'subagent_task', taskId: 's2', subagentType: 'Explore', status: 'running', skipTranscript: true },
    { type: 'proposed_plan', turnId: 't', markdown: '# Proposal\n\n1. Fix it', status: 'completed', isLatest: true },
    { type: 'proposed_plan', turnId: 't', markdown: 'gone', status: 'cleared', isLatest: false },
    { type: 'system_notice', name: 'warn', meta: { message: 'hidden' } },
    text('Fixed. ####### not a heading\n####### also not'),
  ], { modelInfo: { name: 'Opus 5', modelId: 'opus-5' }, endedAt: base + 14 * 1000 + 3_725_000 }),
  entry('assistant', [], { modelInfo: { modelId: 'empty-model' } }),
  entry('user', [text('Thanks 👍'), { type: 'image_group', images: [{ imageId: 'a' }, { imageId: 'b' }] }], { userId: 'u-bo' }),
  entry('assistant', [text('Welcome.'), tool('Edit Makefile', 'edit', [{ type: 'diff', path: 'Makefile', oldText: 'b', newText: 'c' }])], { modelInfo: { name: 'Sonnet' }, endedAt: base + 35 * 1000 + 500 }),
  entry('user', [text('again')], { userId: 'u-ada' }),
  entry('assistant', [text('partial')], { modelInfo: { name: 'Opus 5' }, endedAt: base + 49 * 1000 + 61_000 }),
];

const heavy: any[] = [];
for (let i = 0; i < 3; i++) {
  heavy.push(entry('user', [text(`Question ${i}`)]));
  heavy.push(entry('assistant', [
    { type: 'thought', text: `deliberating ${i} `.repeat(60) },
    tool(`Read src/file-${i}.ts`, 'read', [out(`line ${i} 😀 `.repeat(450))]),
    tool(`Bash ${i}`, 'execute', term('npm', ['install'], `stdout ${i} 中文 `.repeat(350))),
    text(`Answer ${i}`),
  ], { modelInfo: { name: i % 2 ? 'Opus 5' : 'Haiku' } }));
}

const collapsed: any[] = [];
for (let i = 0; i < 6; i++) {
  collapsed.push(entry('user', [text(`Q${i} 问题`)]));
  collapsed.push(entry('assistant', [
    text('Before tools.'),
    { type: 'thought', text: `deliberating ${i} 😀 `.repeat(150) },
    tool(`Read ${i}`, 'read', [out('x'.repeat(1500))]),
    tool(`Grep ${i}`, 'search', [out('y'.repeat(100))]),
    tool(`Edit ${i}`, 'edit', [{ type: 'diff', path: `f${i % 3}.ts`, oldText: '', newText: 'z' }]),
    tool(`Fetch ${i}`, 'fetch', []),
    tool(`Read again ${i}`, 'read', []),
    text(`A${i}`),
  ]));
}

const histories = { small, heavy, collapsed, empty: [entry('assistant', [])] };
const cases = [
  { name: 'small', history: 'small', options: { title: '  Build fix  ', source: 'lbr77/lody-mygo · main', participants: { 'u-ada': 'Ada', 'u-bo': 'Bo' }, incompleteFinalResponse: 'The last response was still generating when copied.' } },
  { name: 'small-solo', history: 'small', options: { participants: { 'u-ada': 'Ada' }, workspaceId: 'ws' } },
  ...[29_750, 27_250, 22_750, 21_500, 20_750, 6_500, 3_250].map((maxChars) => ({ name: `heavy-${maxChars}`, history: 'heavy', options: { maxChars } })),
  { name: 'heavy-recent', history: 'heavy', options: { maxChars: 21_000, recentEntryCount: 2 } },
  ...[18_750, 12_000, 8_000].map((maxChars) => ({ name: `collapsed-${maxChars}`, history: 'collapsed', options: { maxChars, maxTokens: 100_000 } })),
  { name: 'over-budget', history: 'small', options: { maxChars: 50, maxTokens: 10, includeTrimNotice: false, title: 'Tiny' } },
  { name: 'empty', history: 'empty', options: { title: 'Nothing' } },
];
for (const c of cases as any[]) {
  const r = buildConversationMarkdown({ ...c.options, history: (histories as any)[c.history] });
  c.markdown = r.markdown;
  c.stats = r.stats;
}
await Bun.write(process.argv[2], Bun.gzipSync(JSON.stringify({ histories, cases })));
