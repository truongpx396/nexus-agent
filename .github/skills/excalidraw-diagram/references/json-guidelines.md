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

## Spacing

- Leave enough padding inside containers for labels and arrows
- Keep at least 24 to 40 px between neighboring major elements
- Avoid diagonal arrows unless they reduce clutter

## Naming

- Prefer descriptive ids when hand-writing JSON
- Keep related elements near each other in the file for maintainability

## Validation checklist

- every arrow has a clear source and target
- no text overlaps another element
- titles and section labels are visually distinct
- the main path is obvious without reading every label