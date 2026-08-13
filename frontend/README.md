# Nexus Agent — Web Surface

React 19 + Vite + TypeScript web surface (one thin surface adapter over the shared kernel — see
the root [README.md](../README.md) for the platform's architecture).

## Stack

- **Vite** — dev server + build
- **TypeScript** — `tsc -b --noEmit` type-checks as part of `npm run build`
- **Tailwind CSS v4** — wired via the `@tailwindcss/vite` plugin (CSS-first config, no
  `tailwind.config.js`/`postcss.config.js`)
- **TanStack Query** (`@tanstack/react-query`) — server-state/data-fetching
- **ESLint 8** (legacy `.eslintrc.cjs`, not the newer flat-config format) +
  `eslint-plugin-react-hooks` + `eslint-plugin-react-refresh`, with **Prettier** for formatting
  (`eslint-config-prettier` disables ESLint's own formatting rules so the two never conflict)

## Commands

```bash
npm run dev            # start the Vite dev server
npm run build           # type-check + production build
npm run preview         # preview the production build locally
npm run lint             # eslint
npm run format           # prettier --write
npm run format:check     # prettier --check (no writes; what CI/make fmt-ts runs)
```
