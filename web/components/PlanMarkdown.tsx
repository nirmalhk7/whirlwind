import { createElement, Fragment, type ReactNode } from "react";

const headingPattern = /^(#{1,6})\s+(.+)$/;
const listPattern = /^\s*[-*+]\s+/;
type Block = { next: number; node: ReactNode };

function inlineToken(match: RegExpMatchArray, index: number): ReactNode {
  if (match[1]) return <a key={index} href={match[2]} target="_blank" rel="noreferrer">{match[1]}</a>;
  if (match[3]) return <code key={index}>{match[3]}</code>;
  return <strong key={index}>{match[4]}</strong>;
}

function inlineMarkdown(text: string): ReactNode[] {
  const pattern = /\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)|`([^`]+)`|\*\*([^*]+)\*\*/g;
  const nodes: ReactNode[] = [];
  let previous = 0;
  for (const match of text.matchAll(pattern)) {
    const index = match.index ?? 0;
    nodes.push(text.slice(previous, index), inlineToken(match, index));
    previous = index + match[0].length;
  }
  nodes.push(text.slice(previous));
  return nodes;
}

function headingBlock(line: string, index: number): Block | null {
  const heading = headingPattern.exec(line);
  if (!heading) return null;
  return { next: index + 1, node: createElement(`h${Math.min(heading[1].length + 2, 6)}`, { key: index }, inlineMarkdown(heading[2])) };
}

function ChecklistText({ check }: { check: RegExpExecArray }) {
  const completed = check[1].toLowerCase() === "x";
  return <><span aria-label={completed ? "Completed" : "Pending"}>{completed ? "☑" : "☐"}</span>{" "}{inlineMarkdown(check[2])}</>;
}

function listItem(line: string, index: number): ReactNode {
  const text = line.replace(listPattern, "");
  const check = /^\[([ xX])\]\s+(.*)$/.exec(text);
  return <li key={index}>{check ? <ChecklistText check={check} /> : inlineMarkdown(text)}</li>;
}

function listBlock(lines: string[], index: number): Block {
  const start = index;
  const items: ReactNode[] = [];
  while (index < lines.length && listPattern.test(lines[index])) {
    items.push(listItem(lines[index], index));
    index++;
  }
  return { next: index, node: <ul key={start}>{items}</ul> };
}

function paragraphContinues(line: string | undefined): line is string {
  return line !== undefined && Boolean(line.trim()) && !headingPattern.test(line) && !listPattern.test(line);
}

function paragraphBlock(lines: string[], index: number): Block {
  const start = index;
  const paragraph: string[] = [];
  while (paragraphContinues(lines[index])) paragraph.push(lines[index++]);
  const node = <p key={start}>{paragraph.map((text, n) => <Fragment key={n}>{n > 0 && <br />}{inlineMarkdown(text)}</Fragment>)}</p>;
  return { next: index, node };
}

function readBlock(lines: string[], index: number): Block {
  const heading = headingBlock(lines[index], index);
  if (heading) return heading;
  if (listPattern.test(lines[index])) return listBlock(lines, index);
  return paragraphBlock(lines, index);
}

// Markdown text becomes React nodes; embedded HTML remains visible escaped text.
export function PlanMarkdown({ content }: { content: string }) {
  const blocks: ReactNode[] = [];
  const lines = content.replace(/\r\n/g, "\n").split("\n");
  for (let index = 0; index < lines.length;) {
    if (!lines[index].trim()) { index++; continue; }
    const block = readBlock(lines, index);
    blocks.push(block.node);
    index = block.next;
  }
  return <div className="local-plan-document">{blocks}</div>;
}
