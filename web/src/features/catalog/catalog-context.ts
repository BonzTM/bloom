import { createRequiredContext } from "../../lib/react/required-context.js";
import type { CatalogApi } from "./api/catalog-api.js";

const context = createRequiredContext<CatalogApi>("CatalogApi");

export const CatalogApiContext = context.Provider;
export const useCatalogApi = context.useValue;
