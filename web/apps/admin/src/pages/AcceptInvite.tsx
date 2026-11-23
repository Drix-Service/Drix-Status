import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { Button } from "@drix/ui/components/button";
import { Field, Input } from "@drix/ui/components/form";
import { api, ApiError, type Role, type User } from "../api";
import { keys } from "../lib/queries";
import { roleDescription, roleLabel } from "../lib/roles";
import { Logo } from "../components/Shell";

export function AcceptInvitePage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const info = useQuery({
    queryKey: ["invite", token],
    queryFn: () => api.get<{ email: string; role: Role }>(`/invites/accept?token=${encodeURIComponent(token)}`),
    retry: false,
    enabled: !!token,
  });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setErrors({});
    try {
      await api.post<User>("/invites/accept", { token, name, password });
      await qc.invalidateQueries({ queryKey: keys.me });
      navigate("/", { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.fields) setErrors(err.fields);
      else setError(err instanceof ApiError ? err.message : "Could not reach the server");
    } finally {
      setBusy(false);
    }
  };

  const invalid = !token || info.isError;

  return (
    <div className="flex min-h-dvh items-center justify-center bg-subtle px-4">
      <div className="w-full max-w-[24rem]">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <Logo className="size-9" />
          <div>
            <h1 className="text-lg font-semibold tracking-tight">Join the team on Drix</h1>
            {info.data && (
              <p className="mt-1 text-[13px] text-muted-foreground">
                You're invited as <span className="font-medium text-foreground">{roleLabel[info.data.role]}</span>.{" "}
                {roleDescription[info.data.role]}
              </p>
            )}
          </div>
        </div>
        {info.isLoading ? (
          <div className="flex justify-center py-10">
            <Loader2 className="size-5 animate-spin text-muted-foreground" />
          </div>
        ) : invalid ? (
          <div className="grid gap-3 rounded-xl border bg-card p-5 text-center text-[13px] shadow-xs">
            <p>{info.error instanceof ApiError ? info.error.message : "This invite link is missing its token."}</p>
            <Link to="/login" className="font-medium underline underline-offset-4">
              Go to sign in
            </Link>
          </div>
        ) : (
          <form onSubmit={submit} className="grid gap-4 rounded-xl border bg-card p-5 shadow-xs">
            <Field label="Email">
              <Input value={info.data?.email ?? ""} disabled />
            </Field>
            <Field label="Your name" htmlFor="name" error={errors.name}>
              <Input id="name" autoFocus required autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label="Choose a password" htmlFor="password" error={errors.password} hint="At least 10 characters.">
              <Input id="password" type="password" required minLength={10} autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
            {error && <p className="rounded-md bg-destructive/8 px-3 py-2 text-[13px] text-destructive">{error}</p>}
            <Button type="submit" disabled={busy} className="mt-1 w-full">
              {busy && <Loader2 className="animate-spin" />}
              Join team
            </Button>
          </form>
        )}
      </div>
    </div>
  );
}
