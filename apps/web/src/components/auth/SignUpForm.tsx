import { FormEvent } from "react";
import { IconAt, IconLock, IconMail, IconUser } from "../GlassyAuthShell";

type Props = {
  fullName: string;
  username: string;
  email: string;
  password: string;
  confirm: string;
  showPassword: boolean;
  error: string;
  success: string;
  pending: boolean;
  onFullNameChange: (v: string) => void;
  onUsernameChange: (v: string) => void;
  onEmailChange: (v: string) => void;
  onPasswordChange: (v: string) => void;
  onConfirmChange: (v: string) => void;
  onTogglePassword: () => void;
  onSubmit: (e: FormEvent) => void;
};

export function SignUpForm({
  fullName,
  username,
  email,
  password,
  confirm,
  showPassword,
  error,
  success,
  pending,
  onFullNameChange,
  onUsernameChange,
  onEmailChange,
  onPasswordChange,
  onConfirmChange,
  onTogglePassword,
  onSubmit,
}: Props) {
  const mismatch = confirm.length > 0 && password !== confirm;
  const matched = confirm.length > 0 && password === confirm;
  const inputType = showPassword ? "text" : "password";
  const toggleLabel = showPassword ? "Hide passwords" : "Show passwords";
  return (
    <>
      <h2 className="g-title">Create Account</h2>
      <p className="g-lead">Join Bekratos to manage organizations and API keys</p>
      {error ? <div className="g-alert g-alert-error">{error}</div> : null}
      {success ? (
        <div className="g-alert g-alert-success">{success}</div>
      ) : null}
      <form onSubmit={onSubmit}>
        <div className="g-field">
          <label htmlFor="su-name">Full name</label>
          <div className="g-input-row">
            <IconUser />
            <input
              id="su-name"
              type="text"
              required
              placeholder="Alex Johnson"
              value={fullName}
              onChange={(e) => onFullNameChange(e.target.value)}
            />
          </div>
        </div>
        <div className="g-field">
          <label htmlFor="su-username">Username</label>
          <div className="g-input-row">
            <IconAt />
            <input
              id="su-username"
              type="text"
              autoComplete="username"
              required
              minLength={3}
              maxLength={30}
              pattern="[a-zA-Z0-9_]+"
              title="3–30 characters: letters, numbers, underscore"
              placeholder="alex_j"
              value={username}
              onChange={(e) => onUsernameChange(e.target.value)}
            />
          </div>
        </div>
        <div className="g-field">
          <label htmlFor="su-email">Email address</label>
          <div className="g-input-row">
            <IconMail />
            <input
              id="su-email"
              type="email"
              autoComplete="email"
              required
              placeholder="name@domain.com"
              value={email}
              onChange={(e) => onEmailChange(e.target.value)}
            />
          </div>
        </div>
        <div className="g-row-2">
          <div className="g-field">
            <label htmlFor="su-password">Password</label>
            <div className="g-input-row">
              <IconLock />
              <input
                id="su-password"
                type={inputType}
                autoComplete="new-password"
                required
                minLength={8}
                placeholder="••••••••"
                value={password}
                onChange={(e) => onPasswordChange(e.target.value)}
              />
              <button
                type="button"
                className="g-toggle-pw"
                aria-label={toggleLabel}
                aria-pressed={showPassword}
                onClick={onTogglePassword}
              >
                👁
              </button>
            </div>
          </div>
          <div className="g-field">
            <label htmlFor="su-confirm">Confirm</label>
            <div className="g-input-row">
              <IconLock />
              <input
                id="su-confirm"
                type={inputType}
                autoComplete="new-password"
                required
                minLength={8}
                placeholder="••••••••"
                value={confirm}
                onChange={(e) => onConfirmChange(e.target.value)}
              />
              <button
                type="button"
                className="g-toggle-pw"
                aria-label={toggleLabel}
                aria-pressed={showPassword}
                onClick={onTogglePassword}
              >
                👁
              </button>
            </div>
          </div>
        </div>
        <p
          id="su-confirm-hint"
          aria-live="polite"
          className={
            mismatch
              ? "g-hint g-hint-error"
              : matched
                ? "g-hint g-hint-ok"
                : "g-hint"
          }
        >
          {mismatch
            ? "Passwords do not match"
            : matched
              ? "Passwords match"
              : ""}
        </p>
        <button
          className="g-btn-primary"
          type="submit"
          disabled={pending || mismatch}
        >
          {pending ? "Creating…" : "Create Account →"}
        </button>
      </form>
    </>
  );
}
