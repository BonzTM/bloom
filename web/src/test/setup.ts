import "@testing-library/jest-dom/jest-globals";
import { afterAll, afterEach, beforeAll } from "@jest/globals";
import { cleanup } from "@testing-library/react";
import {
  resetMockInvites,
  resetMockMediaServers,
  resetMockNotifications,
  resetMockOwnLinks,
  resetMockRequests,
  resetMockRoleQuotas,
  resetMockSession,
} from "../mocks/handlers.js";
import { server } from "./server.js";

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  cleanup();
  server.resetHandlers();
  resetMockSession();
  resetMockMediaServers();
  resetMockInvites();
  resetMockRequests();
  resetMockRoleQuotas();
  resetMockNotifications();
  resetMockOwnLinks();
});

afterAll(() => {
  server.close();
});
