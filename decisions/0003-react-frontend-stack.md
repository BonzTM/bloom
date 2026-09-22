# 0003. Use the handbook TypeScript/React stack for the SPA

- **Status:** Accepted
- **Date:** 2026-09-21

## Context

ADR 0002 commits to a SPA. The TypeScript handbook fixes the SPA stack: React 19,
Vite, React Router, TanStack Query, Zod, Jest 30 with React Testing Library and
MSW, ESLint with `react-hooks` and `jsx-a11y`, and says these "are mandatory for
greenfield repositories until an accepted ADR says otherwise"
(`typescript/decisions/framework-selection.md`). The owner asked whether Svelte
was worth evaluating and then added: "We chose typescript + react in our handbook,
so let that bias the decision some as well."

Facts gathered in
[research/frontend-react-vs-svelte.md](../docs/research/frontend-react-vs-svelte.md):

- Svelte 5 is stable (2024-10-22) and measurably faster and smaller on the
  krausest benchmark (compressed size 9.7 vs 51.4, swap rows 12.6 vs 89.9).
- Svelte's SPA path is SvelteKit with `ssr = false` and `adapter-static`, which
  the Svelte docs say "has large negative performance and SEO impacts" and is
  "only recommended in certain circumstances"; plain Vite plus Svelte needs a
  third-party router.
- Svelte's official docs and Svelte Testing Library both recommend Vitest; Jest
  with Svelte 5 needs ESM mode, `svelte-jester@5`, and extra transforms. The
  handbook's Jest default would therefore also need an ADR.
- Ecosystem depth for this app's needs is thinner on Svelte: TanStack Table's
  Svelte 5 adapter reached `latest` only on 2026-08-04; shadcn-svelte is an
  "unofficial, community-led" port; weekly downloads run roughly 30:1 in React's
  favour across react-query, react-hook-form, and Recharts versus their Svelte
  counterparts.
- None of eight comparable self-hosted media projects use Svelte; six use React.
- State of JS 2024 reports Svelte retention 0.88 vs React 0.75, so developer
  satisfaction favours Svelte.

## Decision

We use the handbook's TypeScript stack as written: React 19, Vite, React Router
(declarative or data mode, no framework mode), TanStack Query, Zod, Jest 30 with
React Testing Library and MSW, ESLint with `react-hooks` and `jsx-a11y`, Prettier,
npm with a committed lockfile, and the `npm run verify` gate copied from
`typescript/reference/examplefrontend`.

## Consequences

### Good

- Zero deviations from the TypeScript handbook, so its recipes, reference app,
  ESLint config, and Jest setup apply verbatim.
- Largest pool of accessible component primitives, table, chart, and form
  libraries for a data-heavy admin UI.
- Contributors from the Jellyseerr, Jellystat, and Maintainerr communities
  already know the stack.

### Bad

- Larger bundle and slower raw DOM updates than Svelte. For an admin dashboard
  behind a login this is an accepted cost; re-evaluate if bundle size becomes a
  measured user complaint.
- Jest's ESM story remains "experimental", as the handbook itself notes.

### Neutral

- If the handbook later moves to Vitest, Bloom follows through a superseding ADR.

## Alternatives Considered

- **Svelte 5 with SvelteKit in SPA mode.** Rejected: two handbook deviations
  (framework and test runner), thinner ecosystem for tables and accessible UI
  kits, and the Svelte docs' own caveat about SPA mode. Its performance advantage
  does not change outcomes for an authenticated admin UI.
- **Svelte 5 with plain Vite and a community router.** Rejected: the Svelte docs
  point at SvelteKit for anything beyond a toy, and the router choice would be a
  third deviation.
- **Server-rendered Go templates.** Rejected in ADR 0002.

## Links

- **Supersedes:** None.
- Related: ADR 0002.
- Research: `docs/research/frontend-react-vs-svelte.md`.
- Handbook: `typescript/AGENTS.md`, `typescript/decisions/framework-selection.md`,
  `typescript/services/react-applications.md`,
  `typescript/reference/examplefrontend/`.
