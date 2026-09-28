import { useId, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import {
  accountSearchSchema,
  MAX_ACCOUNT_SEARCH_BYTES,
} from "../features/accounts/api/accounts-schemas.js";
import { AccountsTable } from "../features/accounts/components/accounts-table.js";
import { useAccounts } from "../features/accounts/hooks/accounts-queries.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// Every account, searchable by username. The search lives in the URL so a
// page can be shared and reloaded; an invalid one is treated as empty.
export default function AccountsRoute(): ReactNode {
  usePageTitle(pageTitle("Accounts"));
  const session = useSession();
  const viewerId = session.data?.account.id;
  if (viewerId === undefined) {
    return null;
  }
  return <AccountsPage key={viewerId} viewerId={viewerId} />;
}

function readSearch(value: string | null): string {
  if (value === null) {
    return "";
  }
  const parsed = accountSearchSchema.safeParse(value.trim());
  return parsed.success ? parsed.data : "";
}

function AccountsPage({ viewerId }: Readonly<{ viewerId: string }>): ReactNode {
  const [params, setParams] = useSearchParams();
  const search = readSearch(params.get("q"));
  const accounts = useAccounts(viewerId, search);
  const denial = accessDenial(accounts.error);
  useSessionRecheck(denial !== undefined, accounts.errorUpdatedAt);
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Accounts</h1>
      <p className="page-intro">
        Who has a Bloom account, which roles they hold, how they sign in, and
        which media-server users they are linked to. Passwords and identity
        provider details are never shown.
      </p>
      <SearchForm
        search={search}
        onSearch={(next) => {
          setParams(next === "" ? {} : { q: next }, { replace: search !== "" });
        }}
      />
      <AccountsTable query={accounts} search={search} />
    </>
  );
}

function SearchForm({
  search,
  onSearch,
}: Readonly<{ search: string; onSearch: (next: string) => void }>): ReactNode {
  const id = useId();
  return (
    <form
      role="search"
      aria-label="Search accounts"
      className="search-bar"
      onSubmit={(event) => {
        event.preventDefault();
        const input = event.currentTarget.elements.namedItem("q");
        if (input instanceof HTMLInputElement) {
          onSearch(readSearch(input.value));
        }
      }}
    >
      <label htmlFor={`${id}-q`}>Username contains</label>
      <input
        id={`${id}-q`}
        name="q"
        type="search"
        defaultValue={search}
        key={search}
        maxLength={MAX_ACCOUNT_SEARCH_BYTES}
        autoComplete="off"
      />
      <button type="submit">Search</button>
    </form>
  );
}
