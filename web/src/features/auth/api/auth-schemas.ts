import { z } from "zod/v4";

// Wire contracts for the session endpoints under `/api/v1/auth`. Unknown fields
// are stripped so the backend can extend payloads without breaking the UI.
export const accountSchema = z.object({
  id: z.string().min(1).max(200),
  username: z.string().min(1).max(200),
});

export type Account = z.output<typeof accountSchema>;

export const sessionResponseSchema = z.object({
  account: accountSchema,
});

export type SessionResponse = z.output<typeof sessionResponseSchema>;

export const USERNAME_MAX_LENGTH = 64;
export const PASSWORD_MAX_LENGTH = 1024;

// Client-side validation for the sign-in form. It only guards against empty or
// absurdly long input; the server decides whether the credentials are right.
export const loginInputSchema = z.object({
  username: z
    .string()
    .trim()
    .min(1, "Enter your username")
    .max(USERNAME_MAX_LENGTH, "Username is too long"),
  password: z
    .string()
    .min(1, "Enter your password")
    .max(PASSWORD_MAX_LENGTH, "Password is too long"),
});

export type LoginInput = z.output<typeof loginInputSchema>;
