import { z } from "zod";

// Bounds from the contract: a page is at most 100 accounts, a cursor at most
// 2048 characters, and a search at most 128 bytes with no control characters.
export const ACCOUNT_PAGE = 50;
export const MAX_ACCOUNT_CURSOR_LENGTH = 2048;
export const MAX_ACCOUNT_SEARCH_BYTES = 128;

const CONTROL_CHARACTERS = /\p{Cc}/u;

export const accountSearchSchema = z
  .string()
  .refine((value) => !CONTROL_CHARACTERS.test(value), {
    message: "A search cannot contain control characters.",
  })
  .refine(
    (value) =>
      new TextEncoder().encode(value).byteLength <= MAX_ACCOUNT_SEARCH_BYTES,
    { message: "A search is at most 128 bytes." },
  );

export const accountCursorSchema = z
  .string()
  .min(1)
  .max(MAX_ACCOUNT_CURSOR_LENGTH);

export const signInMethodSchema = z.enum(["local", "oidc", "both"]);

export type SignInMethod = z.output<typeof signInMethodSchema>;

export const accountRoleAssignmentSchema = z.object({
  name: z.string().min(1),
  source: z.enum(["manual", "oidc"]),
});

export type AccountRoleAssignment = z.output<
  typeof accountRoleAssignmentSchema
>;

export const accountLinkedMediaUserSchema = z.object({
  media_server_id: z.uuid(),
  media_server_name: z.string().min(1),
  media_user_id: z.string().min(1),
  username: z.string().min(1),
  suppressed: z.boolean(),
});

export type AccountLinkedMediaUser = z.output<
  typeof accountLinkedMediaUserSchema
>;

export const adminAccountSchema = z.object({
  id: z.uuid(),
  username: z.string().min(1),
  created_at: z.string().min(1),
  sign_in_method: signInMethodSchema,
  roles: z.array(accountRoleAssignmentSchema).max(100),
  media_users: z.array(accountLinkedMediaUserSchema).max(100),
  is_self: z.boolean(),
});

export type AdminAccount = z.output<typeof adminAccountSchema>;

export const adminAccountsResponseSchema = z.object({
  items: z.array(adminAccountSchema).max(100),
  next_cursor: z.string().max(MAX_ACCOUNT_CURSOR_LENGTH),
});

export type AdminAccountsPage = z.output<typeof adminAccountsResponseSchema>;

export const accountIdSchema = z.uuid();
