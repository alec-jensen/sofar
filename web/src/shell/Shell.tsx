import { useCallback, useEffect, useState } from "react";
import { LogOut } from "lucide-react";
import "../minimal.css";
import "../dashboard-refresh.css";
import "../buy.css";
import "../design-match.css";
import "../features.css";
import "./shell.css";
import { api, type AuthStatus } from "./api";
import { AuthForm } from "./Auth";
import { NewRecoveryCodes, PasswordForm, RecoveryCodes, TOTPDisable, TOTPFlow } from "./Security";

type Panel = null | "totp" | "totp-off" | "new-codes" | "password" | { codes: string[] };

function Mark() {
  return (
    <span className="mark" aria-hidden="true">
      s
    </span>
  );
}

// The rebuilt app shell. Real screens arrive milestone by milestone (M8); until
// then a signed-in owner sees a placeholder home and the security settings.
export default function Shell() {
  const [status, setStatus] = useState<AuthStatus | null>(null),
    [failed, setFailed] = useState(false),
    [panel, setPanel] = useState<Panel>(null);

  const refresh = useCallback(async () => {
    try {
      setStatus(await api<AuthStatus>("/auth/status"));
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }, []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (failed && !status)
    return (
      <div className="loading">
        <Mark />
        <p>can not reach sofar right now.</p>
        <button className="button" onClick={refresh}>
          try again
        </button>
      </div>
    );
  if (!status)
    return (
      <div className="loading">
        <Mark />
        <p>getting your bearings…</p>
      </div>
    );
  if (!status.authenticated)
    return (
      <div className="auth-shell">
        <div className="auth-brand">
          <Mark />
          <b>sofar</b>
        </div>
        <AuthForm setup={status.setup} onComplete={refresh} />
      </div>
    );

  const done = () => {
    setPanel(null);
    refresh();
  };
  return (
    <main className="shell-home">
      <header className="shell-bar">
        <div className="auth-brand">
          <Mark />
          <b>sofar</b>
        </div>
        <button
          className="text-button"
          onClick={async () => {
            await api("/auth/logout", {}).catch(() => {});
            refresh();
          }}
        >
          <LogOut size={16} /> sign out
        </button>
      </header>

      <section className="card shell-card">
        <span className="eyebrow">rebuilding</span>
        <h1>your numbers will appear here</h1>
        <p>sofar is being rebuilt from the ground up. you are signed in and your account is safe.</p>
      </section>

      <section className="card shell-card" aria-label="security">
        <h2>security</h2>
        {panel === null && (
          <ul className="shell-actions">
            {status.totpEnabled ? (
              <>
                <li>
                  <button className="button" onClick={() => setPanel("new-codes")}>
                    make new recovery codes
                  </button>
                </li>
                <li>
                  <button className="button" onClick={() => setPanel("totp-off")}>
                    turn off two-step verification
                  </button>
                </li>
              </>
            ) : (
              <li>
                <button className="button" onClick={() => setPanel("totp")}>
                  set up two-step verification
                </button>
              </li>
            )}
            <li>
              <button className="button" onClick={() => setPanel("password")}>
                change password
              </button>
            </li>
          </ul>
        )}
        {panel === "totp" && (
          <TOTPFlow
            onDone={(codes) => {
              setPanel({ codes });
              refresh();
            }}
          />
        )}
        {panel === "totp-off" && <TOTPDisable onDone={done} />}
        {panel === "new-codes" && <NewRecoveryCodes onDone={(codes) => setPanel({ codes })} />}
        {panel === "password" && <PasswordForm onDone={done} />}
        {panel !== null && typeof panel === "object" && <RecoveryCodes codes={panel.codes} onDone={done} />}
        {typeof panel === "string" && (
          <button className="text-button" onClick={() => setPanel(null)}>
            cancel
          </button>
        )}
      </section>
    </main>
  );
}
