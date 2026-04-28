/**
 * Maps Clerk SDK errors to user-actionable copy + an optional action link.
 *
 * Shared by the sign-in, sign-up, and forgot-password pages so all three
 * surface a consistent recovery path for the common cross-flow mistakes:
 *
 *   - Signed up via GitHub OAuth, now typing email+password into the sign-in
 *     form → Clerk rejects because no password is on the account. Users see
 *     a cryptic "verification strategy is not valid" default; we translate
 *     that to "set a password" (which routes through /sign-in/forgot-password
 *     via the reset_password_email_code strategy — works even when there's
 *     no password today).
 *
 *   - Signed up via GitHub OAuth, now trying to sign UP again with the same
 *     email → "That email address is taken." Users should sign in, not sign
 *     up; we point them there.
 *
 * The generic fallback is Clerk's longMessage so uncovered errors still
 * render something intelligible instead of the raw object.
 */
export type ClerkErrorInfo = {
  message: string;
  action?: { label: string; href: string };
};

type ClerkError = { code?: string; longMessage?: string; message?: string };

function firstClerkError(err: unknown): ClerkError | null {
  if (err && typeof err === "object" && "errors" in err) {
    const arr = (err as { errors: ClerkError[] }).errors;
    if (Array.isArray(arr) && arr[0]) return arr[0];
  }
  return null;
}

export function describeClerkError(err: unknown): ClerkErrorInfo {
  const ce = firstClerkError(err);
  const code = ce?.code ?? "";
  const fallback =
    ce?.longMessage ?? ce?.message ?? (err instanceof Error ? err.message : "Something went wrong.");

  switch (code) {
    case "form_identifier_exists":
      // Sign-up with a taken email. Almost always an OAuth-first user
      // trying to add password auth via the sign-up form.
      return {
        message: "An account already exists for this email. If you signed up with GitHub, use the button above — or set a password to enable email sign-in.",
        action: { label: "Set a password", href: "/sign-in/forgot-password" },
      };
    case "form_identifier_not_found":
      return {
        message: "No account found for that email.",
        action: { label: "Sign up", href: "/sign-up" },
      };
    case "form_password_incorrect":
      return {
        message: "Password doesn't match.",
        action: { label: "Reset it", href: "/sign-in/forgot-password" },
      };
    case "strategy_for_user_invalid":
    case "verification_strategy_invalid":
    case "form_param_unknown_value":
      // Password attempt on a user whose account has no password set.
      // Happens when someone signed up via GitHub OAuth and later tries
      // email+password sign-in on the same address.
      return {
        message: "This account uses GitHub sign-in. Click \u201CContinue with GitHub\u201D above, or set a password to enable email sign-in.",
        action: { label: "Set a password", href: "/sign-in/forgot-password" },
      };
    default:
      return { message: fallback };
  }
}
