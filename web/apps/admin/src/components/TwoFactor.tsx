import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import qrcode from "qrcode-generator";
import { CheckIcon, CopyIcon, DownloadIcon, FingerprintIcon, KeyRoundIcon, Loader2Icon, PencilIcon, ShieldCheckIcon, SmartphoneIcon, Trash2Icon } from "lucide-react";
import { cn, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge } from "@drix/ui/components/display";
import { Field, Input } from "@drix/ui/components/form";
import { ConfirmDialog, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@drix/ui/components/overlay";
import { api, ApiError, type Passkey, type TwoFactorState } from "../api";
import { keys } from "../lib/queries";
import { createPasskey, deviceName, passkeyError, passkeysSupported } from "../lib/webauthn";

const stateKey = ["two-factor"];

function useTwoFactor() {
  return useQuery({ queryKey: stateKey, queryFn: () => api.get<TwoFactorState>("/auth/two-factor") });
}

/** Your own two-factor settings: authenticator app, passkeys and recovery codes. */
export function TwoFactorPanel() {
  const qc = useQueryClient();
  const { data } = useTwoFactor();
  const [setupOpen, setSetupOpen] = useState(false);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [confirm, setConfirm] = useState<null | "disable" | "regenerate">(null);
  const [removing, setRemoving] = useState<Passkey | null>(null);
  const [renaming, setRenaming] = useState<Passkey | null>(null);
  const [adding, setAdding] = useState(false);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: stateKey });
    qc.invalidateQueries({ queryKey: keys.me });
  };

  const addPasskey = async () => {
    setAdding(true);
    try {
      const options = await api.post<{ publicKey: Record<string, unknown> }>("/auth/passkeys/register");
      const cred = await createPasskey(options);
      const r = await api.post<{ passkey: Passkey; recovery_codes: string[] | null }>(
        `/auth/passkeys/register/finish?name=${encodeURIComponent(deviceName())}`,
        cred,
      );
      toast.success(`Passkey added: ${r.passkey.name}`);
      if (r.recovery_codes?.length) setCodes(r.recovery_codes);
      refresh();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : passkeyError(e);
      if (msg) toast.error(msg);
    } finally {
      setAdding(false);
    }
  };

  const remove = useMutation({
    mutationFn: (p: Passkey) => api.del(`/auth/passkeys/${p.id}`),
    onSuccess: () => {
      toast.success("Passkey removed");
      refresh();
    },
    onError: (e) => toast.error(e.message),
  });

  if (!data) return <div className="h-40 animate-pulse rounded-lg bg-muted/40" />;
  const on = data.totp || data.passkeys.length > 0;

  return (
    <div className="grid gap-4">
      <div className={cn("flex items-center gap-2.5 rounded-lg border px-3 py-2.5 text-[13px]", on ? "bg-muted/30" : "border-degraded/40 bg-degraded/5")}>
        <ShieldCheckIcon className={cn("size-4 shrink-0", on ? "text-up" : "text-degraded")} />
        <p className="flex-1">
          {on ? (
            <>Two-factor sign-in is <strong className="font-medium">on</strong>. Signing in needs your password and one of the ways below.</>
          ) : (
            <>Two-factor sign-in is <strong className="font-medium">off</strong>. Add a passkey or an authenticator app so a stolen password isn't enough.</>
          )}
        </p>
      </div>

      {/* Passkeys */}
      <div className="grid gap-2">
        <div className="flex items-center gap-2">
          <FingerprintIcon className="size-4 text-muted-foreground" />
          <h3 className="flex-1 text-[13px] font-medium">Passkeys</h3>
          {passkeysSupported() && (
            <Button type="button" size="sm" variant="outline" disabled={adding} onClick={addPasskey}>
              {adding && <Loader2Icon className="animate-spin" />}
              Add a passkey
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">Sign in with Face ID, Touch ID, Windows Hello or a security key. No code to type, and it works as both steps.</p>
        {data.passkeys.length > 0 && (
          <ul className="divide-y rounded-lg border">
            {data.passkeys.map((p) => (
              <li key={p.id} className="flex items-center gap-3 px-3 py-2 text-[13px]">
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{p.name}</span>
                  <span className="block text-xs text-muted-foreground">
                    Added {timeAgo(p.created_at)} · {p.last_used_at ? `used ${timeAgo(p.last_used_at)}` : "not used yet"}
                  </span>
                </span>
                <Button type="button" variant="ghost" size="icon-sm" aria-label={`Rename ${p.name}`} onClick={() => setRenaming(p)}>
                  <PencilIcon />
                </Button>
                <Button type="button" variant="ghost" size="icon-sm" aria-label={`Remove ${p.name}`} className="hover:text-destructive" onClick={() => setRemoving(p)}>
                  <Trash2Icon />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* Authenticator app */}
      <div className="grid gap-2">
        <div className="flex items-center gap-2">
          <SmartphoneIcon className="size-4 text-muted-foreground" />
          <h3 className="flex-1 text-[13px] font-medium">Authenticator app</h3>
          {data.totp ? (
            <>
              <Badge variant="muted" className="gap-1">
                <CheckIcon className="size-3" /> On
              </Badge>
              <Button type="button" size="sm" variant="ghost" onClick={() => setConfirm("disable")}>
                Turn off
              </Button>
            </>
          ) : (
            <Button type="button" size="sm" variant="outline" disabled={!data.can_totp} onClick={() => setSetupOpen(true)}>
              Set up
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          {data.can_totp ? "A 6-digit code from 1Password, Google Authenticator, Authy or the iPhone's Passwords app." : "Needs SECRET_KEY on the server. Passkeys work without it."}
        </p>
      </div>

      {/* Recovery codes */}
      {on && (
        <div className="grid gap-2">
          <div className="flex items-center gap-2">
            <KeyRoundIcon className="size-4 text-muted-foreground" />
            <h3 className="flex-1 text-[13px] font-medium">Recovery codes</h3>
            <Button type="button" size="sm" variant="ghost" onClick={() => setConfirm("regenerate")}>
              Make new codes
            </Button>
          </div>
          <p className={cn("text-xs", data.recovery_left <= 2 ? "text-degraded" : "text-muted-foreground")}>
            {data.recovery_left} of 10 left. Each works once if you lose your phone or passkeys.
          </p>
        </div>
      )}

      {setupOpen && (
        <TotpSetupDialog
          onClose={() => setSetupOpen(false)}
          onEnabled={(c) => {
            setSetupOpen(false);
            toast.success("Authenticator app turned on");
            if (c?.length) setCodes(c);
            refresh();
          }}
        />
      )}
      {codes && <RecoveryCodesDialog codes={codes} onClose={() => setCodes(null)} />}
      {confirm && (
        <PasswordConfirmDialog
          title={confirm === "disable" ? "Turn off the authenticator app?" : "Make new recovery codes?"}
          description={confirm === "disable" ? "Enter your password to confirm." : "Your old codes stop working. Enter your password to confirm."}
          action={confirm === "disable" ? "Turn off" : "Make new codes"}
          destructive={confirm === "disable"}
          onClose={() => setConfirm(null)}
          run={async (password) => {
            if (confirm === "disable") {
              await api.post("/auth/two-factor/totp/disable", { password });
              toast.success("Authenticator app turned off");
            } else {
              const r = await api.post<{ recovery_codes: string[] }>("/auth/two-factor/recovery-codes", { password });
              setCodes(r.recovery_codes);
            }
            setConfirm(null);
            refresh();
          }}
        />
      )}
      {renaming && <RenameDialog passkey={renaming} onClose={() => setRenaming(null)} onDone={refresh} />}
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={`Remove ${removing?.name}?`}
        description="You won't be able to sign in with it any more. You can add it again later."
        confirmLabel="Remove passkey"
        destructive
        onConfirm={() => removing && remove.mutate(removing)}
      />
    </div>
  );
}

function QrCode({ text }: { text: string }) {
  const path = useMemo(() => {
    const qr = qrcode(0, "M");
    qr.addData(text);
    qr.make();
    const n = qr.getModuleCount();
    let d = "";
    for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (qr.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`;
    return { d, n };
  }, [text]);
  // Always dark on white: scanners need the contrast, whatever the theme.
  return (
    <svg viewBox={`-2 -2 ${path.n + 4} ${path.n + 4}`} className="size-44 rounded-lg bg-white" shapeRendering="crispEdges" role="img" aria-label="QR code for your authenticator app">
      <path d={path.d} fill="#09090b" />
    </svg>
  );
}

function TotpSetupDialog({ onClose, onEnabled }: { onClose: () => void; onEnabled: (codes: string[] | null) => void }) {
  const { data, error } = useQuery({
    queryKey: ["totp-setup"],
    queryFn: () => api.post<{ secret: string; uri: string }>("/auth/two-factor/totp/setup"),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  });
  const [code, setCode] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const enable = useMutation({
    mutationFn: () => api.post<{ recovery_codes: string[] | null }>("/auth/two-factor/totp/enable", { code }),
    onSuccess: (r) => onEnabled(r.recovery_codes),
    onError: (e) => {
      setErr(e instanceof ApiError && e.fields?.code ? e.fields.code : e.message);
      setCode("");
    },
  });
  const grouped = data?.secret.match(/.{1,4}/g)?.join(" ") ?? "";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            enable.mutate();
          }}
        >
          <DialogHeader>
            <DialogTitle>Set up an authenticator app</DialogTitle>
            <DialogDescription>Scan the code with your app, then type the 6-digit code it shows.</DialogDescription>
          </DialogHeader>
          {error ? (
            <p className="rounded-md bg-destructive/8 px-3 py-2 text-[13px] text-destructive">{error.message}</p>
          ) : !data ? (
            <div className="mx-auto size-44 animate-pulse rounded-lg bg-muted" />
          ) : (
            <div className="grid justify-items-center gap-3">
              <a href={data.uri} aria-label="Open in an authenticator app on this device">
                <QrCode text={data.uri} />
              </a>
              <div className="text-center">
                <p className="text-xs text-muted-foreground">Can't scan it? Enter this key:</p>
                <p className="mt-1 font-mono text-[13px] tracking-wide select-all">{grouped}</p>
              </div>
            </div>
          )}
          <Field label="Code from the app" htmlFor="totp-code" error={err ?? undefined}>
            <Input
              id="totp-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={7}
              placeholder="123 456"
              className="text-center font-mono text-lg tracking-[0.3em]"
              value={code}
              onChange={(e) => {
                setErr(null);
                setCode(e.target.value.replace(/[^0-9]/g, "").slice(0, 6));
              }}
            />
          </Field>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={code.length !== 6 || enable.isPending || !data}>
              {enable.isPending && <Loader2Icon className="animate-spin" />}
              Turn on
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function RecoveryCodesDialog({ codes, onClose }: { codes: string[]; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const text = `Drix Status recovery codes (${new Date().toLocaleDateString()})\nEach code works once.\n\n${codes.join("\n")}\n`;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md" onInteractOutside={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>Save your recovery codes</DialogTitle>
          <DialogDescription>
            If you lose your phone or passkeys, each code signs you in once. Keep them in a password manager. This is the only time they're shown.
          </DialogDescription>
        </DialogHeader>
        <ul className="grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-lg border bg-muted/30 px-5 py-4 font-mono text-[13px]">
          {codes.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={async () => {
              await navigator.clipboard.writeText(text);
              setCopied(true);
            }}
          >
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? "Copied" : "Copy"}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              const a = document.createElement("a");
              a.href = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
              a.download = "drix-recovery-codes.txt";
              a.click();
              URL.revokeObjectURL(a.href);
            }}
          >
            <DownloadIcon />
            Download
          </Button>
        </div>
        <DialogFooter>
          <Button type="button" onClick={onClose}>
            I've saved them
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PasswordConfirmDialog({
  title,
  description,
  action,
  destructive,
  onClose,
  run,
}: {
  title: string;
  description: string;
  action: string;
  destructive?: boolean;
  onClose: () => void;
  run: (password: string) => Promise<void>;
}) {
  const [password, setPassword] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-sm">
        <form
          className="grid gap-4"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setErr(null);
            try {
              await run(password);
            } catch (e) {
              setErr(e instanceof ApiError && e.fields?.password ? e.fields.password : e instanceof Error ? e.message : "Something went wrong");
            } finally {
              setBusy(false);
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          <Field label="Password" htmlFor="confirm-pw" error={err ?? undefined}>
            <Input id="confirm-pw" type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant={destructive ? "destructive" : "default"} disabled={!password || busy}>
              {busy && <Loader2Icon className="animate-spin" />}
              {action}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RenameDialog({ passkey, onClose, onDone }: { passkey: Passkey; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(passkey.name);
  const save = useMutation({
    mutationFn: () => api.put(`/auth/passkeys/${passkey.id}`, { name }),
    onSuccess: () => {
      onDone();
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-sm">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <DialogHeader>
            <DialogTitle>Rename passkey</DialogTitle>
          </DialogHeader>
          <Field label="Name" htmlFor="pk-name">
            <Input id="pk-name" autoFocus maxLength={60} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!name.trim() || save.isPending}>
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** Shown instead of the admin when the team requires two-factor sign-in and you haven't set it up. */
export function TwoFactorSetupScreen({ onSignOut }: { onSignOut: () => void }) {
  const { data } = useTwoFactor();
  const qc = useQueryClient();
  const done = !!data && (data.totp || data.passkeys.length > 0);
  return (
    <div className="flex min-h-dvh items-start justify-center bg-subtle px-4 py-12 sm:items-center">
      <div className="w-full max-w-lg">
        <div className="mb-6 text-center">
          <ShieldCheckIcon className="mx-auto size-8 text-muted-foreground" />
          <h1 className="mt-3 text-lg font-semibold tracking-tight">Set up two-factor sign-in</h1>
          <p className="mt-1 text-[13px] text-muted-foreground">
            Your team requires it. Add a passkey (quickest) or an authenticator app, then continue.
          </p>
        </div>
        <div className="rounded-xl border bg-card p-5 shadow-xs">
          <TwoFactorPanel />
        </div>
        <div className="mt-4 flex items-center justify-between">
          <Button type="button" variant="ghost" size="sm" onClick={onSignOut}>
            Sign out
          </Button>
          <Button type="button" disabled={!done} onClick={() => qc.invalidateQueries({ queryKey: keys.me })}>
            Continue
          </Button>
        </div>
      </div>
    </div>
  );
}
