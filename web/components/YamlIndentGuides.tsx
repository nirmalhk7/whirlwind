export function YamlIndentGuides({ content, trailingLine = false }: { content: string; trailingLine?: boolean }) {
  const lines = content.split("\n");
  if (!trailingLine && content.endsWith("\n")) lines.pop();
  const indents = lines.map(line => {
    let column = 0;
    for (const character of line.match(/^[ \t]*/)?.[0] ?? "") {
      column += character === "\t" ? 2 - column % 2 : 1;
    }
    return column;
  });
  const smallest = indents.reduce((step, column) => column > 0 ? Math.min(step, column) : step, Infinity);
  const step = Number.isFinite(smallest) ? smallest : 2;
  return <span className="yaml-indentation-lines" aria-hidden="true">
    {indents.map((indent, index) => <span className="yaml-guide-line" data-line={index} key={index}>
      {Array.from({ length: Math.min(64, Math.ceil(indent / step)) }, (_, level) =>
        <span className="yaml-indent-guide" key={level} style={{ left: `${level * step}ch` }} />)}
    </span>)}
  </span>;
}
