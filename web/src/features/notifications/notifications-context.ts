import { createRequiredContext } from "../../lib/react/required-context.js";
import type { NotificationsApi } from "./api/notifications-api.js";

const context = createRequiredContext<NotificationsApi>("NotificationsApi");

export const NotificationsApiContext = context.Provider;
export const useNotificationsApi = context.useValue;
