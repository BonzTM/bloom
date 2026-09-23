import { useId, type ReactNode, type SyntheticEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { hasPermission, permissions } from "../features/auth/permissions.js";
import { searchQuerySchema } from "../features/requests/api/metadata-schemas.js";
import {
  mediaKindSchema,
  type MediaKind,
} from "../features/requests/api/requests-schemas.js";
import { MyRequests } from "../features/requests/components/my-requests.js";
import { describeSearchError } from "../features/requests/components/request-errors.js";
import { kindLabel } from "../features/requests/components/request-format.js";
import { TitleGrid } from "../features/requests/components/title-grid.js";
import {
  useRequests,
  useSearchTitles,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function DiscoverRoute(): ReactNode {
  usePageTitle(pageTitle("Requests"));
  const session = useSession();
  const accountId = session.data?.account.id;
  const granted = session.data?.permissions ?? [];
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes.
  if (accountId === undefined) {
    return null;
  }
  return (
    <DiscoverPage
      key={accountId}
      accountId={accountId}
      canSearch={hasPermission(granted, permissions.requestsCreate)}
      canReadOwn={hasPermission(granted, permissions.requestsReadOwn)}
    />
  );
}

type DiscoverPageProps = Readonly<{
  accountId: string;
  canSearch: boolean;
  canReadOwn: boolean;
}>;

function DiscoverPage({
  accountId,
  canSearch,
  canReadOwn,
}: DiscoverPageProps): ReactNode {
  const [params, setParams] = useSearchParams();
  const query = readQuery(params.get("q"));
  const kind = readKind(params.get("kind"));
  const results = useSearchTitles(accountId, canSearch ? query : "", kind);
  const mine = useRequests(accountId, { requesterId: accountId }, canReadOwn);
  const denial = accessDenial(results.error) ?? accessDenial(mine.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(results.errorUpdatedAt, mine.errorUpdatedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Requests</h1>
      <p className="page-intro">
        Find a movie or series and ask for it. Requests wait for approval unless
        your account approves its own, and become available once the download
        finishes.
      </p>
      {canSearch ? (
        <section aria-labelledby="search-heading" className="card">
          <h2 id="search-heading">Search</h2>
          <SearchForm
            query={query}
            kind={kind}
            onSearch={(next) => {
              setParams(next, { replace: query !== "" });
            }}
          />
          <SearchResults query={query} results={results} />
        </section>
      ) : null}
      {canReadOwn ? (
        <section aria-labelledby="my-requests-heading" className="card">
          <h2 id="my-requests-heading">My requests</h2>
          <MyRequests query={mine} />
        </section>
      ) : null}
    </>
  );
}

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value : "";
}

function readQuery(raw: string | null): string {
  const parsed = searchQuerySchema.safeParse(raw ?? "");
  return parsed.success ? parsed.data : "";
}

function readKind(raw: string | null): MediaKind | undefined {
  const parsed = mediaKindSchema.safeParse(raw);
  return parsed.success ? parsed.data : undefined;
}

type SearchFormProps = Readonly<{
  query: string;
  kind: MediaKind | undefined;
  onSearch: (params: Record<string, string>) => void;
}>;

// The query lives in the URL, so a result page can be shared and the back
// button returns to it. Nothing is sent until the form is submitted.
function SearchForm({ query, kind, onSearch }: SearchFormProps): ReactNode {
  const id = useId();
  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const next = readQuery(text(data.get("q")));
    const nextKind = readKind(text(data.get("kind")));
    if (next === "") {
      return;
    }
    onSearch(
      nextKind === undefined ? { q: next } : { q: next, kind: nextKind },
    );
  }
  return (
    <form
      role="search"
      aria-label="Search titles"
      onSubmit={handleSubmit}
      noValidate
    >
      <div className="search-row">
        <label htmlFor={`${id}-q`}>Title</label>
        <input
          id={`${id}-q`}
          name="q"
          type="search"
          autoComplete="off"
          maxLength={200}
          defaultValue={query}
          key={query}
        />
        <label htmlFor={`${id}-kind`}>Kind</label>
        <select id={`${id}-kind`} name="kind" defaultValue={kind ?? ""}>
          <option value="">Movies and series</option>
          {mediaKindSchema.options.map((option) => (
            <option key={option} value={option}>
              {kindLabel(option)}
            </option>
          ))}
        </select>
        <button type="submit" className="btn-primary">
          Search
        </button>
      </div>
    </form>
  );
}

function SearchResults({
  query,
  results,
}: Readonly<{
  query: string;
  results: ReturnType<typeof useSearchTitles>;
}>): ReactNode {
  if (query === "") {
    return <p>Type a title to search The Movie Database.</p>;
  }
  if (results.status === "pending") {
    return <AsyncStatus>Searching for {query}…</AsyncStatus>;
  }
  if (results.status === "error") {
    return (
      <>
        <AsyncStatus kind="alert">
          {describeSearchError(results.error)}
        </AsyncStatus>
        <button
          type="button"
          onClick={() => {
            void results.refetch();
          }}
        >
          Retry
        </button>
      </>
    );
  }
  if (results.data.length === 0) {
    return <p role="status">Nothing matches {query}.</p>;
  }
  return (
    <>
      <p role="status">
        {String(results.data.length)} result
        {results.data.length === 1 ? "" : "s"} for {query}.
      </p>
      <TitleGrid label={`Results for ${query}`} titles={results.data} />
    </>
  );
}
