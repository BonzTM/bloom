import { createRequiredContext } from "../../lib/react/required-context.js";
import type { InvitesApi } from "./api/invites-api.js";

const context = createRequiredContext<InvitesApi>("InvitesApi");

export const InvitesApiContext = context.Provider;
export const useInvitesApi = context.useValue;
