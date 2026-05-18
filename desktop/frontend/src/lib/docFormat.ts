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
