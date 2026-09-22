import { createContext, useContext } from "react";
import type { SystemApi } from "./api/system-api.js";

export const SystemApiContext = createContext<SystemApi | undefined>(undefined);

export function useSystemApi(): SystemApi {
  const api = useContext(SystemApiContext);
  if (api === undefined) {
    throw new Error("SystemApi provider is missing");
  }
  return api;
}
