import { createRequiredContext } from "../../lib/react/required-context.js";
import type { AuthApi } from "./api/auth-api.js";

const context = createRequiredContext<AuthApi>("AuthApi");

export const AuthApiContext = context.Provider;
export const useAuthApi = context.useValue;
