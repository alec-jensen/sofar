import { useState, type FormEvent } from "react";
import { ArrowRight, Loader2 } from "lucide-react";
import { api } from "./api";

// Sign in, or create the one account on a fresh install.
export function AuthForm({ setup, onComplete }: { setup: boolean; onComplete: () => void }) {
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  return (
    <form
      className="card auth-card"
      onSubmit={async (e: FormEvent<HTMLFormElement>) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        const d = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>;
        try {
          await api(setup ? "/auth/setup" : "/auth/login", d);
          onComplete();
        } catch (err) {
          setError((err as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <span className="eyebrow">a little clarity for your money</span>
      <h1>{setup ? "set up sofar" : "welcome back"}</h1>
      <p>{setup ? "create the one account for your own sofar." : "pick up where you left off."}</p>
      {setup && (
        <label className="field">
          setup key
          <input name="setupKey" type="password" required autoComplete="off" />
          <small>the SOFAR_SETUP_KEY from your server configuration.</small>
        </label>
      )}
      <label className="field">
        username
        <input name="username" required autoComplete="username" />
      </label>
      <label className="field">
        password
        <input
          name="password"
          type="password"
          required
          minLength={setup ? 12 : 1}
          autoComplete={setup ? "new-password" : "current-password"}
        />
      </label>
      {!setup && (
        <label className="field">
          authenticator or recovery code (if enabled)
          <input name="code" autoComplete="one-time-code" maxLength={11} />
        </label>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <button className="button primary full" disabled={busy}>
        {busy ? <Loader2 className="spin" size={18} /> : setup ? "create your account" : "sign in"}
        <ArrowRight size={17} />
      </button>
    </form>
  );
}
