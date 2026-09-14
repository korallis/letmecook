import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Marked, Renderer } from 'marked';

const directory = dirname(fileURLToPath(import.meta.url));
const documents = [
  { id: 'readme', file: 'README.md', title: 'Overview', description: 'Intent, ownership and first release' },
  { id: 'evaluation', file: 'evaluation.md', title: 'Evaluation', description: 'Challenges, research and decisions' },
  { id: 'prd', file: 'PRD.md', title: 'Requirements', description: 'Journey, scope and success measures' },
  { id: 'spec', file: 'spec.md', title: 'Specification', description: 'Authority, execution and recovery' },
  { id: 'roadmap', file: 'roadmap.md', title: 'Roadmap', description: 'Milestones and evidence gates' },
];
const byFile = new Map(documents.map(document => [document.file, document.id]));
const escape = (text: string) => text.replace(/[&<>"']/g, character => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
})[character]!);
const slug = (text: string) => text.toLowerCase().normalize('NFKD')
  .replace(/[^\w\s-]/g, '').trim().replace(/\s+/g, '-').replace(/-+/g, '-');

const sections: string[] = [];
const navigation: string[] = [];
for (const document of documents) {
  const headings: { id: string; depth: number; text: string }[] = [];
  const slugs = new Map<string, number>();
  const renderer = new Renderer();
  renderer.heading = function ({ tokens, depth, text }) {
    const base = slug(text.replace(/`/g, ''));
    const occurrence = slugs.get(base) ?? 0;
    slugs.set(base, occurrence + 1);
    const id = `${document.id}-${base}${occurrence ? `-${occurrence}` : ''}`;
    headings.push({ id, depth, text: text.replace(/`/g, '') });
    return `<h${depth} id="${id}">${this.parser.parseInline(tokens)}</h${depth}>\n`;
  };
  renderer.link = function ({ href, title, tokens }) {
    const [file, fragment] = href.split('#', 2);
    if (file === 'index.html') href = '#top';
    else if (byFile.has(file)) href = `#${byFile.get(file)}${fragment ? `-${fragment}` : ''}`;
    else if (href.startsWith('#')) href = `#${document.id}-${fragment}`;
    else if (!/^https?:\/\//.test(href)) throw new Error(`Unsupported link in ${document.file}: ${href}`);
    return `<a href="${escape(href)}"${title ? ` title="${escape(title)}"` : ''}>${this.parser.parseInline(tokens)}</a>`;
  };
  // These are reviewed prose sources. Escape raw HTML so future edits cannot
  // accidentally add active markup to the standalone documentation reader.
  renderer.html = ({ text }) => escape(text);
  const markdown = await readFile(join(directory, document.file), 'utf8');
  const parsed = new Marked({ renderer, gfm: true }).parse(markdown, { async: false });
  const html = parsed.replace(/<table>/g, '<div class="table-wrap" tabindex="0" role="region" aria-label="Scrollable table"><table>')
    .replace(/<\/table>/g, '</table></div>');
  sections.push(`<article class="doc" id="${document.id}" data-doc="${document.id}">${html}</article>`);
  const links = headings.filter(heading => heading.depth === 2 || heading.depth === 3)
    .map(heading => `<li class="lvl${heading.depth}"><a href="#${heading.id}">${escape(heading.text)}</a></li>`).join('\n');
  navigation.push(`<section class="navdoc" data-doc="${document.id}"><a class="navtitle" href="#${document.id}">${document.title}<em>${document.description}</em></a><ul class="navlist">${links}</ul></section>`);
}

const template = await readFile(join(directory, 'reader-template.html.in'), 'utf8');
for (const marker of ['{{NAV}}', '{{ARTICLES}}']) {
  if (template.split(marker).length !== 2) throw new Error(`Expected one ${marker} marker`);
}
const output = template.replace('{{NAV}}', () => navigation.join('\n'))
  .replace('{{ARTICLES}}', () => sections.join('\n'));
const ids = [...output.matchAll(/\sid="([^"]+)"/g)].map(match => match[1]);
const idSet = new Set(ids);
if (idSet.size !== ids.length) throw new Error('Duplicate HTML IDs');
for (const [, target] of output.matchAll(/href="#([^"]+)"/g)) {
  if (!idSet.has(target)) throw new Error(`Broken internal link: #${target}`);
}

const outputPath = join(directory, 'index.html');
if (process.argv.includes('--check')) {
  if (await readFile(outputPath, 'utf8') !== output) {
    throw new Error('index.html is stale; run npm --prefix docs run build');
  }
  console.log(`Reader verified: ${documents.length} documents, ${ids.length} unique anchors, all internal links valid.`);
} else {
  await writeFile(outputPath, output);
  console.log(`Reader generated from ${documents.length} Markdown documents.`);
}
