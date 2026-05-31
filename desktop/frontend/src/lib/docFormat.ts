// isSvgDoc detects whether a Documents-view payload should render as an
// SVG image rather than be passed to the markdown editor. Filename suffix
// is the primary signal; for filenames without `.svg` (e.g. an SVG
// imported under a generic name) we sniff the first ~2 KiB of the body
// for an `<svg` root.
export function isSvgDoc(filename: string, content: string): boolean {
  if (filename.toLowerCase().endsWith('.svg')) return true;
  const head = content.slice(0, 2048).trimStart().toLowerCase();
  if (head.startsWith('<svg')) return true;
  if (head.startsWith('<?xml') && head.includes('<svg')) return true;
  return false;
}

// isHtmlDoc detects whether a Documents-view payload should render as an
// HTML document (in a sandboxed iframe) rather than be passed to the
// markdown editor — the read surface for the design agent's HTML
// wireframes (BACI-298). Filename suffix is the primary signal; for
// filenames without `.html`/`.htm` we sniff the first ~2 KiB of the body
// for an `<!doctype html` or `<html` root. Mutually exclusive with
// isSvgDoc in practice: an SVG root is `<svg`, never `<html`, so the
// suffix + root-sniff already separate the two — no extra guard needed.
export function isHtmlDoc(filename: string, content: string): boolean {
  const f = filename.toLowerCase();
  if (f.endsWith('.html') || f.endsWith('.htm')) return true;
  const head = content.slice(0, 2048).trimStart().toLowerCase();
  if (head.startsWith('<!doctype html')) return true;
  if (head.startsWith('<html')) return true;
  return false;
}
