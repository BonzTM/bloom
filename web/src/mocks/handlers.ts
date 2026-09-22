import { http, HttpResponse } from "msw";
import type { VersionInfo } from "../features/system/api/system-schemas.js";

export const mockVersion: VersionInfo = {
  name: "bloom",
  version: "0.1.0-dev",
  commit: "0123456789abcdef0123456789abcdef01234567",
};

export const handlers = [
  http.get("*/api/v1/version", () => HttpResponse.json(mockVersion)),
];
