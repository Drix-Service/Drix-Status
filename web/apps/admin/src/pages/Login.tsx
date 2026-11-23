import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon, FingerprintIcon, KeyRoundIcon, Loader2, SmartphoneIcon } from "lucide-react";
import { Button } from "@drix/ui/components/button";
import { Field, Input } from "@drix/ui/components/form";
import { api, ApiError, type LoginResult, type User } from "../api";
import { keys } from "../lib/queries";
import { getPasskey, passkeyError, passkeysSupported } from "../lib/webauthn";
import { Logo } from "../components/Shell";

type Step = { ticket: string; methods: ("totp" | "passkey" | "recovery")[] };
type Mode = "totp" | "recovery";

export function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"password" | "passkey" | "code" | null>(null);
  const [step, setStep] = useState<Step | null>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const canPasskey = passkeysSupported();

  const done = async () => {
    await qc.invalidateQueries({ queryKey: keys.me });
    const from = (location.state as { from?: string } | null)?.from;
    navigate(from && from !== "/login" ? from : "/", { replace: true });
  };

  const fail = (err: unknown) => {
    if (err instanceof ApiError && err.code === "ticket_expired") setStep(null);
    setError(err instanceof ApiError ? err.message : "Could not reach the server");
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy("password");
    setError(null);
    try {
      const r = await api.post<LoginResult>("/auth/login", { email, password });
      if ("ticket" in r) {
        setStep({ ticket: r.ticket, methods: r.methods });
        setPassword("");
      } else {
        await done();
      }
    } catch (err) {
      fail(err);
    } finally {
      setBusy(null);
    }
  };

  const passkeySignIn = async () => {
    setBusy("passkey");
    setError(null);
    try {
      const { id, options } = await api.post<{ id: string; options: { publicKey: Record<string, unknown> } }>("/auth/passkey");
      const cred = await getPasskey(options);
      await api.post<User>(`/auth/passkey/finish?id=${encodeURIComponent(id)}`, cred);
      await done();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : passkeyError(err);
      if (msg) setError(msg);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="flex min-h-dvh items-center justify-center bg-subtle px-4">
      <div className="w-full max-w-[22rem]">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <Logo className="size-9" />
          <div>
            <h1 className="text-lg font-semibold tracking-tight">{step ? "Confirm it's you" : "Sign in to Drix"}</h1>
            <p className="mt-1 text-[13px] text-muted-foreground">
              {step ? "Your account uses two-factor sign-in." : "Manage monitors, workers and your status page."}
            </p>
          </div>
        </div>

        {step ? (
          <SecondStep step={step} busy={busy} setBusy={setBusy} error={error} setError={setError} onDone={done} onFail={fail} onBack={() => { setStep(null); setError(null); }} />
        ) : (
          <>
            <form onSubmit={submit} className="grid gap-4 rounded-xl border bg-card p-5 shadow-xs">
              <Field label="Email" htmlFor="email">
                <Input id="email" type="email" autoComplete="username webauthn" autoFocus required value={email} onChange={(e) => setEmail(e.target.value)} />
              </Field>
              <Field label="Password" htmlFor="password">
                <Input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  aria-invalid={!!error}
                />
              </Field>
              {error && <p className="rounded-md bg-destructive/8 px-3 py-2 text-[13px] text-destructive">{error}</p>}
              <Button type="submit" disabled={!!busy} className="mt-1 w-full">
                {busy === "password" && <Loader2 className="animate-spin" />}
                Sign in
              </Button>
            </form>
            {canPasskey && (
              <Button type="button" variant="outline" className="mt-3 w-full bg-card" disabled={!!busy} onClick={passkeySignIn}>
                {busy === "passkey" ? <Loader2 className="animate-spin" /> : <FingerprintIcon />}
                Sign in with a passkey
              </Button>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function SecondStep({
  step,
  busy,
  setBusy,
  error,
  setError,
  onDone,
  onFail,
  onBack,
}: {
  step: Step;
  busy: string | null;
  setBusy: (b: "passkey" | "code" | null) => void;
  error: string | null;
  setError: (e: string | null) => void;
  onDone: () => Promise<void>;
  onFail: (e: unknown) => void;
  onBack: () => void;
}) {
  const hasTotp = step.methods.includes("totp");
  const hasPasskey = step.methods.includes("passkey") && passkeysSupported();
  const [mode, setMode] = useState<Mode>(hasTotp ? "totp" : "recovery");
  const [code, setCode] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const triedPasskey = useRef(false);

  useEffect(() => input.current?.focus(), [mode]);

  const send = async (body: { code?: string; recovery_code?: string }) => {
    setBusy("code");
    setError(null);
    try {
      await api.post<User>("/auth/login/2fa", { ticket: step.ticket, ...body });
      await onDone();
    } catch (err) {
      setCode("");
      onFail(err);
    } finally {
      setBusy(null);
    }
  };

  const passkey = async () => {
    setBusy("passkey");
    setError(null);
    try {
      const options = await api.post<{ publicKey: Record<string, unknown> }>("/auth/login/2fa/passkey", { ticket: step.ticket });
      const cred = await getPasskey(options);
      await api.post<User>(`/auth/login/2fa/passkey/finish?ticket=${encodeURIComponent(step.ticket)}`, cred);
      await onDone();
    } catch (err) {
      if (err instanceof ApiError) onFail(err);
      else {
        const msg = passkeyError(err);
        if (msg) setError(msg);
      }
    } finally {
      setBusy(null);
    }
  };

  // With only passkeys (no app), open the passkey prompt straight away.
  useEffect(() => {
    if (!hasTotp && hasPasskey && !triedPasskey.current) {
      triedPasskey.current = true;
      void passkey();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="grid gap-3">
      <form
        className="grid gap-4 rounded-xl border bg-card p-5 shadow-xs"
        onSubmit={(e) => {
          e.preventDefault();
          void send(mode === "totp" ? { code } : { recovery_code: code });
        }}
      >
        {mode === "totp" ? (
          <Field label="Code from your authenticator app" htmlFor="otp">
            <Input
              ref={input}
              id="otp"
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9 ]*"
              maxLength={7}
              placeholder="123 456"
              className="text-center font-mono text-lg tracking-[0.3em]"
              value={code}
              onChange={(e) => {
                const v = e.target.value.replace(/[^0-9]/g, "").slice(0, 6);
                setCode(v);
                if (v.length === 6 && !busy) void send({ code: v });
              }}
              aria-invalid={!!error}
            />
          </Field>
        ) : (
          <Field label="Recovery code" htmlFor="recovery" hint="One of the codes you saved when you turned on two-factor sign-in.">
            <Input ref={input} id="recovery" autoComplete="off" spellCheck={false} placeholder="abcd-efgh" className="font-mono" value={code} onChange={(e) => setCode(e.target.value)} />
          </Field>
        )}
        {error && <p className="rounded-md bg-destructive/8 px-3 py-2 text-[13px] text-destructive">{error}</p>}
        <Button type="submit" disabled={!!busy || !code} className="w-full">
          {busy === "code" && <Loader2 className="animate-spin" />}
          Continue
        </Button>
      </form>

      <div className="grid gap-1.5 rounded-xl border bg-card p-1.5 shadow-xs">
        {hasPasskey && (
          <OtherWay icon={busy === "passkey" ? <Loader2 className="animate-spin" /> : <FingerprintIcon />} disabled={!!busy} onClick={passkey}>
            Use a passkey
          </OtherWay>
        )}
        {hasTotp && mode !== "totp" && (
          <OtherWay icon={<SmartphoneIcon />} disabled={!!busy} onClick={() => { setMode("totp"); setCode(""); setError(null); }}>
            Use your authenticator app
          </OtherWay>
        )}
        {mode !== "recovery" && (
          <OtherWay icon={<KeyRoundIcon />} disabled={!!busy} onClick={() => { setMode("recovery"); setCode(""); setError(null); }}>
            Use a recovery code
          </OtherWay>
        )}
        <OtherWay icon={<ArrowLeftIcon />} disabled={!!busy} onClick={onBack}>
          Sign in as someone else
        </OtherWay>
      </div>
    </div>
  );
}

function OtherWay({ icon, children, disabled, onClick }: { icon: React.ReactNode; children: React.ReactNode; disabled?: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="flex items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[13px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50 [&_svg]:size-4"
    >
      {icon}
      {children}
    </button>
  );
}
