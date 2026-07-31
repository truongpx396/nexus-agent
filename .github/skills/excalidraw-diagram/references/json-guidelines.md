# Excalidraw JSON Guidelines

Use valid Excalidraw JSON with a top-level structure similar to:

```json
{
  "type": "excalidraw",
  "version": 2,
  "source": "nexus-agent-excalidraw-skill",
  "elements": [],
  "appState": {
    "viewBackgroundColor": "#fcfbf7"
  },
  "files": {}
}
```

## Element guidance

- Use rectangles for containers and system blocks
- Use diamonds sparingly for decisions or approval gates
- Use arrows for directional flow
- Use text elements for labels and annotations

## Arrows and connectors

- **A multi-point arrow MUST set `"roundness": null`.** For a linear element a
  non-null `roundness` (e.g. `{"type": 2}`) makes Excalidraw draw a smooth curve
  *through* the waypoints instead of straight segments between them, so an
  intended right-angle elbow renders as an S-bend. Two-point arrows have no
  interior waypoint and look the same either way — which is why the defect only
  appears once you start routing around elements.
- **Bind both ends** so connectors follow their shapes when a box is moved. A
  binding is two-sided: set it on the arrow *and* mirror it onto the shape.

  ```json
  // on the arrow
  "startBinding": { "elementId": "queue", "focus": 0, "gap": 4 },
  "endBinding":   { "elementId": "worker", "focus": 0, "gap": 4 }

  // on each bound shape
  "boundElements": [ { "id": "arrow_q_w", "type": "arrow" } ]
  ```

  An arrow whose binding names a missing element, or a shape whose
  `boundElements` names a missing arrow, is a dangling reference — check both
  directions before rendering.

## Spacing

- Leave enough padding inside containers for labels and arrows
- Keep at least 24 to 40 px between neighboring major elements
- Avoid diagonal arrows unless they reduce clutter

## Naming

- Prefer descriptive ids when hand-writing JSON
- Keep related elements near each other in the file for maintainability

## Validation checklist

- every arrow has a clear source and target
- every multi-point arrow has `"roundness": null` (otherwise its elbows curve)
- every arrow binding resolves, and is mirrored in the shape's `boundElements`
- no text overlaps another element
- titles and section labels are visually distinct
- the main path is obvious without reading every label