# bloom-web

The Bloom user interface: a React 19 and Vite single-page application that the Go binary embeds and serves from the same origin as the JSON API (ADR 0002, ADR 0003).

## What It Is

- React 19 function components with React Router route composition, a lazy `/about` route, an explicit not-found route, Suspense status UI, router errors, and a final class render-error boundary.
- TanStack Query-owned server state with a resource query-key factory and bounded retry defaults.
- A single typed fetch boundary with URL ownership, JSON headers, a ten-second timeout, caller cancellation, one-megabyte response bounds, safe HTTP error mapping, and Zod-parsed responses.
- Two feature modules: `system` consumes `GET /api/v1/version` and renders the server version badge; `auth` signs in and out against `/api/v1/auth` and shows the current session in the navigation.
- Jest 30's Babel transform with jsdom, React Testing Library, user-event, and MSW. Tests use accessible roles and names and reject every unhandled request.

## Requirements

- Node.js 24.18.0 (pinned in `.nvmrc`)
- npm and the committed `package-lock.json`
- The Bloom backend on port 8080 for live API development, or the opt-in MSW browser worker

## Setup And Run

```bash
npm ci
cp .env.example .env
npm run dev
```

Vite proxies `/api`, `/livez`, and `/readyz` to `http://localhost:8080`. To work entirely offline, set `VITE_ENABLE_MSW=true`; the browser then starts the committed `public/mockServiceWorker.js` and uses the same contract-validating handlers as the Jest suite. Recreate that generated worker after an MSW upgrade with `npx msw init public --save`. The worker never ships in the binary: `scripts/postbuild.mjs` removes it from the build output unless `VITE_ENABLE_MSW=true` was set at build time, and restores the `.gitkeep` the Go embed needs.

`VITE_API_BASE_URL` is optional and defaults to the page origin, which is correct both under the Vite proxy and inside the Go binary. Like every Vite-exposed value, it is public configuration and must never contain a secret.

## Package Map

| Path                              | Responsibility                                                                             |
| --------------------------------- | ------------------------------------------------------------------------------------------ |
| `src/app/`                        | router, providers, QueryClient defaults, Suspense, and error-boundary composition          |
| `src/routes/`                     | home, lazy about, lazy sign-in, and not-found navigation boundaries and page titles        |
| `src/features/auth/`              | sign-in and sign-out: wire schemas, API slice, session query, login form, session controls |
| `src/features/system/api/`        | `/api/v1/version` Zod wire schema and API slice                                            |
| `src/features/system/hooks/`      | query keys and the version query                                                           |
| `src/features/system/components/` | the version badge                                                                          |
| `src/components/`                 | shared presentation-only status UI                                                         |
| `src/lib/api/`                    | bounded fetch, abort propagation, response parsing, and typed errors                       |
| `src/mocks/`                      | shared browser/test MSW handlers                                                           |
| `src/test/`                       | jsdom polyfills, MSW lifecycle, and application render composition                         |

## Verification

The canonical offline gate runs formatting, ESLint with React Hooks and jsx-a11y at zero warnings, strict type checking, Jest with at most two workers, the high-severity npm audit policy, and the Vite production build:

```bash
npm run verify
# equivalent shim
make verify
```

Tests cover the home page and its version badge, navigation to the lazy about route, cold deep links, the not-found route, page-title updates, focus after navigation, sign-in success and every rejection path, the return destination after sign-in including hostile values, session and sign-out races against late responses, sign-out failure, version endpoint failure and contract rejection, Zod response rejection, non-JSON and oversized responses, caller abort, client timeout, backend error-envelope mapping with Retry-After, and MSW's rejection of unhandled requests.

## Build And Delivery

`npm run build` writes fingerprinted static assets to `../internal/api/web/dist/` (see `vite.config.ts`). The Go binary embeds that directory and serves `index.html` for client-side routes, so this package has no Dockerfile and no server of its own. The `base` path is `/`; HTML cache policy, CSP, and other security headers are the backend's responsibility.

## Related Documents

- [ADR 0002](../decisions/0002-embed-spa-in-binary.md): ship the UI as a Vite-built SPA embedded in the Go binary.
- [ADR 0003](../decisions/0003-react-frontend-stack.md): use the handbook TypeScript/React stack.
- [Requirements](../docs/requirements.md): the three functional areas the home page previews.
