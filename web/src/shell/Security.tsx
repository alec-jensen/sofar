import { useEffect, useState, type FormEvent } from "react";
import { ArrowUpRight, Copy, Loader2, ShieldCheck } from "lucide-react";
import QRCode from "qrcode";
import { api } from "./api";

type Done = () => void;

function useSubmit(fn: (d: FormData) => Promise<void>) {
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await fn(new FormData(e.currentTarget));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return { error, busy, submit };
}

function Submit({ busy, children, danger }: { busy: boolean; children: string; danger?: boolean }) {
  return (
    <button className={"button primary full" + (danger ? " danger" : "")} disabled={busy}>
      {busy ? <Loader2 className="spin" size={18} /> : children}
    </button>
  );
}

// Recovery codes are shown once, so the owner confirms they have saved them.
export function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: Done }) {
  const [saved, setSaved] = useState(false);
  return (
    <div>
      <ShieldCheck />
      <h2>your recovery codes</h2>
      <p>
        each code signs you in once if you lose your authenticator. they are shown only now, so keep them somewhere
        safe.
      </p>
      <ul className="recovery-codes" aria-label="recovery codes">
        {codes.map((c) => (
          <li key={c}>
            <code>{c}</code>
          </li>
        ))}
      </ul>
      <button
        className="text-button"
        type="button"
        onClick={() => navigator.clipboard?.writeText(codes.join("\n")).catch(() => {})}
      >
        <Copy size={16} /> copy all
      </button>
      <label className="check">
        <input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />i have saved these codes
      </label>
      <button className="button primary full" disabled={!saved} onClick={onDone}>
        done
      </button>
    </div>
  );
}

// Enrolling an authenticator changes how you sign in, so it asks for the password again.
export function TOTPFlow({ onDone }: { onDone: (codes: string[]) => void }) {
  const [started, setStarted] = useState<{ secret: string; url: string; password: string } | null>(null);
  if (!started) return <TOTPStart onStarted={setStarted} />;
  return <TOTPConfirm {...started} onDone={onDone} />;
}

function TOTPStart({ onStarted }: { onStarted: (s: { secret: string; url: string; password: string }) => void }) {
  const { error, busy, submit } = useSubmit(async (d) => {
    const password = String(d.get("password"));
    const r = await api<{ secret: string; url: string }>("/auth/totp/setup", { password });
    onStarted({ ...r, password });
  });
  return (
    <form onSubmit={submit}>
      <ShieldCheck />
      <h2>set up two-step verification</h2>
      <p>enter your password to continue.</p>
      <label className="field">
        password
        <input name="password" type="password" required autoComplete="current-password" />
      </label>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <Submit busy={busy}>continue</Submit>
    </form>
  );
}

function TOTPConfirm({
  secret,
  url,
  password,
  onDone,
}: {
  secret: string;
  url: string;
  password: string;
  onDone: (codes: string[]) => void;
}) {
  const [qr, setQr] = useState("");
  useEffect(() => {
    QRCode.toDataURL(url, { margin: 1, width: 220, color: { dark: "#24332b", light: "#ffffff" } })
      .then(setQr)
      .catch(() => setQr(""));
  }, [url]);
  const { error, busy, submit } = useSubmit(async (d) => {
    const r = await api<{ recoveryCodes: string[] }>("/auth/totp/confirm", { code: d.get("code"), password });
    onDone(r.recoveryCodes);
  });
  return (
    <form onSubmit={submit}>
      <ShieldCheck />
      <h2>two-step verification</h2>
      <p>
        scan this with your authenticator app, or enter the secret by hand as a time-based code. then enter the
        six-digit code it shows.
      </p>
      {qr && <img className="totp-qr" src={qr} width={220} height={220} alt="QR code for your authenticator app" />}
      <code className="totp-secret">{secret}</code>
      <a className="text-button" href={url}>
        open authenticator app
        <ArrowUpRight size={16} />
      </a>
      <label className="field">
        six-digit code
        <input name="code" required pattern="[0-9]{6}" inputMode="numeric" autoComplete="one-time-code" maxLength={6} />
      </label>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <Submit busy={busy}>enable two-step verification</Submit>
    </form>
  );
}

export function TOTPDisable({ onDone }: { onDone: Done }) {
  const { error, busy, submit } = useSubmit(async (d) => {
    await api("/auth/totp/disable", { password: d.get("password"), code: d.get("code") });
    onDone();
  });
  return (
    <form onSubmit={submit}>
      <ShieldCheck />
      <h2>turn off two-step verification?</h2>
      <p>sign-in will only need your password. confirm it is you first.</p>
      <label className="field">
        password
        <input name="password" type="password" required autoComplete="current-password" />
      </label>
      <label className="field">
        six-digit code
        <input name="code" required pattern="[0-9]{6}" inputMode="numeric" autoComplete="one-time-code" maxLength={6} />
      </label>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <Submit busy={busy} danger>
        turn it off
      </Submit>
    </form>
  );
}

export function NewRecoveryCodes({ onDone }: { onDone: (codes: string[]) => void }) {
  const { error, busy, submit } = useSubmit(async (d) => {
    const r = await api<{ recoveryCodes: string[] }>("/auth/recovery-codes", { password: d.get("password") });
    onDone(r.recoveryCodes);
  });
  return (
    <form onSubmit={submit}>
      <ShieldCheck />
      <h2>make new recovery codes</h2>
      <p>this replaces your old codes. enter your password to continue.</p>
      <label className="field">
        password
        <input name="password" type="password" required autoComplete="current-password" />
      </label>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <Submit busy={busy}>make new codes</Submit>
    </form>
  );
}

export function PasswordForm({ onDone }: { onDone: Done }) {
  const { error, busy, submit } = useSubmit(async (d) => {
    if (d.get("next") !== d.get("confirm")) throw new Error("the new passwords do not match.");
    await api("/auth/password", { current: d.get("current"), next: d.get("next") });
    onDone();
  });
  return (
    <form onSubmit={submit}>
      <ShieldCheck />
      <h2>change your password</h2>
      <p>your other devices will be signed out.</p>
      <label className="field">
        current password
        <input name="current" type="password" required autoComplete="current-password" />
      </label>
      <label className="field">
        new password
        <input name="next" type="password" required minLength={12} maxLength={72} autoComplete="new-password" />
        <small>at least 12 characters.</small>
      </label>
      <label className="field">
        new password again
        <input name="confirm" type="password" required minLength={12} maxLength={72} autoComplete="new-password" />
      </label>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <Submit busy={busy}>change password</Submit>
    </form>
  );
}
