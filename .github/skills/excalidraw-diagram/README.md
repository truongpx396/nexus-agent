# Excalidraw Diagram Skill

This workspace skill helps generate Excalidraw diagrams for workflows, architectures, protocols, and concept explanations.

## Included files

- `SKILL.md`: workflow and usage guidance for the agent
- `references/color-palette.md`: style tokens and usage rules
- `references/layout-patterns.md`: layout choices by diagram type
- `references/json-guidelines.md`: minimal Excalidraw JSON guidance
- `references/render_excalidraw.py`: optional local renderer
- `references/render_template.html`: browser template used by the renderer
- `references/pyproject.toml`: renderer dependencies

## Optional local renderer setup

```bash
cd .github/skills/excalidraw-diagram/references
uv sync
uv run playwright install chromium
```

## Render a diagram

```bash
cd .github/skills/excalidraw-diagram/references
uv run python render_excalidraw.py path/to/diagram.excalidraw path/to/diagram.png
```

The renderer is only for validation. The main artifact is still the `.excalidraw` JSON file.