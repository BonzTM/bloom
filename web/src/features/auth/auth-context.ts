import { createContext, useContext } from "react";
import type { AuthApi } from "./api/auth-api.js";

export const AuthApiContext = createContext<AuthApi | undefined>(undefined);

export function useAuthApi(): AuthApi {
  const api = useContext(AuthApiContext);
  if (api === undefined) {
    throw new Error("AuthApi provider is missing");
  }
  return api;
}
