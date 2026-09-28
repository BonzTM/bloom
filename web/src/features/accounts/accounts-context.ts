import { createRequiredContext } from "../../lib/react/required-context.js";
import type { AccountsApi } from "./api/accounts-api.js";

const context = createRequiredContext<AccountsApi>("AccountsApi");

export const AccountsApiContext = context.Provider;
export const useAccountsApi = context.useValue;
