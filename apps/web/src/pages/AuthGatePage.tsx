import { FormEvent, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { ApiError, authApi } from "../api/client";
import { SignInForm } from "../components/auth/SignInForm";
import { SignUpForm } from "../components/auth/SignUpForm";
import { GlassyAuthShell } from "../components/GlassyAuthShell";
import { useAuth } from "../lib/auth-context";
import { setTokens } from "../lib/auth-storage";

type Mode = "signin" | "signup";

export function AuthGatePage() {
  const location = useLocation();
  const navigate = useNavigate();
  const { refreshUser } = useAuth();
  const initialMode: Mode = location.pathname.includes("register")
    ? "signup"
    : "signin";
  const [mode, setMode] = useState<Mode>(initialMode);

  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [signInError, setSignInError] = useState("");
  const [signInPending, setSignInPending] = useState(false);

  const [fullName, setFullName] = useState("");
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [regPassword, setRegPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [signUpError, setSignUpError] = useState("");
  const [signUpSuccess, setSignUpSuccess] = useState("");
  const [signUpPending, setSignUpPending] = useState(false);

  function onModeChange(next: Mode) {
    setMode(next);
    navigate(next === "signup" ? "/register" : "/login", { replace: true });
  }

  async function onSignIn(e: FormEvent) {
    e.preventDefault();
    setSignInError("");
    setSignInPending(true);
    try {
      const tokens = await authApi.login(identifier.trim(), password);
      setTokens(tokens.accessToken, tokens.refreshToken);
      await refreshUser();
      navigate("/app");
    } catch (err) {
      setSignInError(err instanceof ApiError ? err.message : "Login failed");
    } finally {
      setSignInPending(false);
    }
  }

  async function onSignUp(e: FormEvent) {
    e.preventDefault();
    setSignUpError("");
    setSignUpSuccess("");
    if (regPassword !== confirm) {
      setSignUpError("Passwords do not match");
      return;
    }
    setSignUpPending(true);
    try {
      const res = await authApi.register({
        email: email.trim(),
        username: username.trim().toLowerCase(),
        password: regPassword,
        fullName: fullName.trim(),
      });
      setSignUpSuccess(
        `Account created (${res.status}). Check your email to verify before signing in.`,
      );
    } catch (err) {
      setSignUpError(
        err instanceof ApiError ? err.message : "Registration failed",
      );
    } finally {
      setSignUpPending(false);
    }
  }

  return (
    <GlassyAuthShell
      mode={mode}
      onModeChange={onModeChange}
      signIn={
        <SignInForm
          identifier={identifier}
          password={password}
          showPassword={showPassword}
          error={signInError}
          pending={signInPending}
          onIdentifierChange={setIdentifier}
          onPasswordChange={setPassword}
          onTogglePassword={() => setShowPassword((v) => !v)}
          onSubmit={onSignIn}
        />
      }
      signUp={
        <SignUpForm
          fullName={fullName}
          username={username}
          email={email}
          password={regPassword}
          confirm={confirm}
          error={signUpError}
          success={signUpSuccess}
          pending={signUpPending}
          onFullNameChange={setFullName}
          onUsernameChange={setUsername}
          onEmailChange={setEmail}
          onPasswordChange={setRegPassword}
          onConfirmChange={setConfirm}
          onSubmit={onSignUp}
          onTogglePassword={() => setShowPassword((v) => !v)}
          showPassword={showPassword}
        />
      }
    />
  );
}
