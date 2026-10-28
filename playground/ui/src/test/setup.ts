// jsdom lays nothing out, and has no Range geometry at all; CodeMirror
// measures text through it after every update. Empty geometry is what an
// unlaid-out page has, and keeps those measurements from throwing. Tests in
// the node environment have no DOM to patch.
if (typeof Range !== "undefined") {
  const none = (): DOMRectList => Object.assign([], { item: () => null }) as unknown as DOMRectList;
  const zero = (): DOMRect => ({ x: 0, y: 0, width: 0, height: 0, top: 0, right: 0, bottom: 0, left: 0, toJSON: () => ({}) }) as DOMRect;
  Range.prototype.getClientRects ??= none;
  Range.prototype.getBoundingClientRect ??= zero;
}
