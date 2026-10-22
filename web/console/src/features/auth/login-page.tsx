import { useNavigate, useSearch } from "@tanstack/react-router";
import { KeyRound } from "lucide-react";
import { useEffect, useRef, useState, type SyntheticEvent } from "react";
import { describeError, isAbortError, ApiError } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import { setSession } from "../../lib/session/store";
import { safeRedirect } from "../../lib/validation";
import { Wordmark } from "../../components/logo";
import { Button } from "../../components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { FieldError, Input, Label } from "../../components/ui/form";
import { Alert } from "../../components/ui/feedback";

/** Used when the node does not send Retry-After with its 429 response. */
const DEFAULT_RATE_LIMIT_SECONDS = 60;
const MAX_CREDENTIAL_LENGTH = 256;
const GENERIC_FAILURE = "Sign-in failed. Check the access key and secret key, then try again.";

/**
 * Signs in with an access key and a secret key. The secret is read from the
 * field once, sent in the request body, and cleared. It is never stored.
 */
export function LoginPage() {
  const search = useSearch({ from: "/login" });
  const navigate = useNavigate();
  const secretRef = useRef<HTMLInputElement>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const [accessKey, setAccessKey] = useState("");
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [cooldownUntil, setCooldownUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    return () => {
      controllerRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (cooldownUntil <= Date.now()) {
      return;
    }
    const timer = setInterval(() => {
      setNow(Date.now());
    }, 1000);
    return () => {
      clearInterval(timer);
    };
  }, [cooldownUntil]);

  const cooldownSeconds = Math.max(0, Math.ceil((cooldownUntil - now) / 1000));
  const locked = pending || cooldownSeconds > 0;

  async function onSubmit(event: SyntheticEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (locked) {
      return;
    }
    const key = accessKey.trim();
    const secret = secretRef.current?.value ?? "";
    // Clear the field at once, so the secret does not stay in the DOM after submit.
    if (secretRef.current) {
      secretRef.current.value = "";
    }
    setFailure(null);

    if (key === "" || secret === "") {
      setFieldError("Enter both the access key and the secret key.");
      return;
    }
    if (key.length > MAX_CREDENTIAL_LENGTH || secret.length > MAX_CREDENTIAL_LENGTH) {
      setFieldError("The access key or secret key is too long.");
      return;
    }
    setFieldError(null);
    setPending(true);
    const controller = new AbortController();
    controllerRef.current = controller;
    try {
      const created = await api.login(key, secret, controller.signal);
      setSession({
        status: "signed-in",
        accessKey: created.accessKey,
        expiresAt: created.expiresAt,
        csrf: created.csrf,
      });
      await navigate({ href: safeRedirect(search.redirect) });
    } catch (error) {
      if (isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && (error.status === 429 || error.code === "SlowDown")) {
        const seconds = error.retryAfterSeconds ?? DEFAULT_RATE_LIMIT_SECONDS;
        setCooldownUntil(Date.now() + seconds * 1000);
        setNow(Date.now());
        setFailure(`Too many sign-in attempts. Wait ${seconds} seconds, then try again.`);
      } else if (error instanceof ApiError && error.status === 403) {
        setFailure(GENERIC_FAILURE);
      } else {
        setFailure(describeError(error));
      }
    } finally {
      controllerRef.current = null;
      setPending(false);
    }
  }

  return (
    <div className="flex min-h-dvh items-center justify-center p-4">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex justify-center">
          <Wordmark className="text-2xl" />
        </div>
        <Card className="p-6" data-testid="login-card">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-lg">
              <KeyRound className="text-primary size-5" aria-hidden="true" />
              Sign in to the console
            </CardTitle>
            <CardDescription>Use an administrator access key. The secret is never stored in this browser.</CardDescription>
          </CardHeader>

          <form
            onSubmit={(event) => {
              void onSubmit(event);
            }}
            className="flex flex-col gap-4"
            noValidate
          >
            <div className="flex flex-col gap-2">
              <Label htmlFor="login-access-key">Access key</Label>
              <Input
                id="login-access-key"
                name="accessKey"
                autoComplete="username"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                maxLength={MAX_CREDENTIAL_LENGTH}
                value={accessKey}
                onChange={(event) => {
                  setAccessKey(event.target.value);
                }}
                aria-invalid={fieldError !== null}
                aria-describedby={fieldError ? "login-field-error" : undefined}
                required
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="login-secret-key">Secret key</Label>
              <Input
                ref={secretRef}
                id="login-secret-key"
                name="secretKey"
                type="password"
                autoComplete="off"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                maxLength={MAX_CREDENTIAL_LENGTH}
                defaultValue=""
                aria-invalid={fieldError !== null}
                aria-describedby={fieldError ? "login-field-error" : undefined}
                required
              />
            </div>
            <FieldError id="login-field-error" message={fieldError} />

            {failure ? (
              <Alert tone="danger">
                <span>{failure}</span>
              </Alert>
            ) : null}

            <Button type="submit" size="lg" className="w-full" disabled={locked}>
              {pending ? "Signing in…" : cooldownSeconds > 0 ? `Try again in ${cooldownSeconds} s` : "Sign in"}
            </Button>
          </form>
        </Card>
        <p className="text-muted-foreground text-center text-xs">
          Sessions last 12 hours. Sign out on shared computers.
        </p>
      </div>
    </div>
  );
}
