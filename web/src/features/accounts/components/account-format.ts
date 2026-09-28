import type {
  AccountRoleAssignment,
  SignInMethod,
} from "../api/accounts-schemas.js";

export function signInMethodLabel(method: SignInMethod): string {
  switch (method) {
    case "local":
      return "Password";
    case "oidc":
      return "Single sign-on";
    case "both":
      return "Password and single sign-on";
  }
}

// "admin, viewer (SSO)": manual assignments plain, provider-managed marked.
export function rolesLabel(roles: readonly AccountRoleAssignment[]): string {
  if (roles.length === 0) {
    return "None";
  }
  return roles
    .map((role) => (role.source === "oidc" ? `${role.name} (SSO)` : role.name))
    .join(", ");
}

export function formatCreated(createdAt: string): string {
  return createdAt.slice(0, 10);
}
