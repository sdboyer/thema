// jsdom lays nothing out, and its Range (jsdom 25) has neither of the
// measurements CodeMirror takes of the text it renders. Answer with no
// rectangles, so that tests can edit in an editor without the measuring
// pass failing in an animation frame after the test.
const noRect = { x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, toJSON: () => ({}) } as DOMRect;
Range.prototype.getClientRects = () => [] as unknown as DOMRectList;
Range.prototype.getBoundingClientRect = () => noRect;
