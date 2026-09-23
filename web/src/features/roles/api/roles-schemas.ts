import { z } from "zod/v4";
import { permissionSchema } from "../../auth/api/auth-schemas.js";

// Wire contract for `GET /api/v1/roles`, mirroring `Role` and `RolesResponse`
// in api/openapi.yaml. Unknown response fields are stripped so the backend can
// extend payloads without breaking the UI.
const MAX_PAGE_ITEMS = 100;
const MAX_CURSOR_LENGTH = 86;

export const roleSchema = z.object({
  id: z.uuid(),
  name: z.string(),
  description: z.string(),
  built_in: z.boolean(),
  created_at: z.iso.datetime({ offset: true }),
  permissions: z.array(permissionSchema),
});

export type Role = z.output<typeof roleSchema>;

// An empty `next_cursor` marks the final page.
export const rolesPageSchema = z.object({
  items: z.array(roleSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_CURSOR_LENGTH),
});

export type RolesPage = z.output<typeof rolesPageSchema>;

export const rolesCursorSchema = z.string().min(1).max(MAX_CURSOR_LENGTH);

export const roleIdSchema = z.uuid();
